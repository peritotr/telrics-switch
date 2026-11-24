// SPDX-License-Identifier: AGPL-3.0-only

// Package rating turns call durations into money. All amounts are int64
// micro-units; nothing here uses floating point.
package rating

import (
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"time"
)

// Micro is the number of micro-units in one currency unit.
const Micro = 1_000_000

// Rate is the billing rule for one destination prefix.
type Rate struct {
	Prefix       string
	PricePerMin  int64
	ConnectFee   int64
	InitialSec   int
	IncrementSec int
	GraceSec     int
	Blocked      bool

	// When the rate applies. A rate with a window goes before one without
	// for the same prefix whenever a call starts inside it.
	Window
	// EffectiveFrom and ID settle which of several rates wins: the one in
	// force most recently, then the one entered first.
	EffectiveFrom time.Time
	ID            int64
}

// Window limits something to days of the week and hours of the day. Days is
// a set of bits, Monday = 1 up to Sunday = 64 (0 and 127: every day). From
// and To are minutes since midnight and the window ends just before To;
// equal values mean the whole day, From after To a window across midnight,
// which counts to the day it starts on.
type Window struct {
	Days     uint8
	From, To int
}

// EveryDay is the Days value of a window without a limit on days.
const EveryDay = 127

// Limited reports whether the window leaves any time out.
func (w Window) Limited() bool {
	return w.Days != 0 && w.Days != EveryDay || w.From != w.To
}

// Covers reports whether local, a time in the window's own time zone, falls
// inside it. A date the calendar lists counts as the day of the week it
// says (a public holiday as a Sunday, say); the calendar is read in the
// same time zone, and a window across midnight looks up the date it began
// on. A nil calendar has no such dates.
func (w Window) Covers(local time.Time, cal Calendar) bool {
	minute := local.Hour()*60 + local.Minute()
	began := false // the window began the day before local
	switch {
	case w.From < w.To:
		if minute < w.From || minute >= w.To {
			return false
		}
	case w.From > w.To:
		if minute < w.To {
			began = true
		} else if minute < w.From {
			return false
		}
	}
	if w.Days == 0 || w.Days == EveryDay {
		return true
	}
	var wd time.Weekday
	switch {
	case !began:
		wd = cal.Weekday(local)
	case len(cal) == 0:
		wd = (local.Weekday() + 6) % 7
	default:
		wd = cal.Weekday(local.AddDate(0, 0, -1))
	}
	day := (int(wd) + 6) % 7 // Monday = 0
	return w.Days&(1<<day) != 0
}

// Calendar holds the dates that count as another day of the week, public
// holidays mostly: by date, the day they count as. It is read only; the
// engine shares one between calls.
type Calendar map[Date]time.Weekday

// Date is a day of the calendar, in no particular time zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf is the date of t in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

// Weekday is the day of the week t counts as: its own, unless its date (in
// t's location) is in the calendar.
func (c Calendar) Weekday(t time.Time) time.Weekday {
	if len(c) > 0 {
		if as, ok := c[DateOf(t)]; ok {
			return as
		}
	}
	return t.Weekday()
}

// Tidy prepares the rates of one prefix that are in force for Select: of
// those with the same window only the one in force most recently stays, and
// the rest are put in the order they are tried.
func Tidy(list []Rate) []Rate {
	slices.SortFunc(list, func(a, b Rate) int {
		if c := b.EffectiveFrom.Compare(a.EffectiveFrom); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	kept := list[:0]
	for _, r := range list {
		if !slices.ContainsFunc(kept, func(k Rate) bool { return k.Window == r.Window }) {
			kept = append(kept, r)
		}
	}
	return kept
}

// Select picks from the tidied rates of one prefix the one for a call
// starting at local: a rate whose window covers that moment, else the rate
// without a window, else none. cal is the calendar of the tariff's account
// (nil: no holidays).
func Select(list []Rate, local time.Time, cal Calendar) (Rate, bool) {
	var whole *Rate
	for i := range list {
		switch r := &list[i]; {
		case !r.Limited():
			if whole == nil {
				whole = r
			}
		case r.Covers(local, cal):
			return *r, true
		}
	}
	if whole != nil {
		return *whole, true
	}
	return Rate{}, false
}

// BilledSeconds applies grace, the initial period and the increment.
func (r Rate) BilledSeconds(duration int) int {
	if duration <= 0 || duration <= r.GraceSec {
		return 0
	}
	if duration <= r.InitialSec {
		return r.InitialSec
	}
	inc := r.IncrementSec
	if inc < 1 {
		inc = 1
	}
	over := duration - r.InitialSec
	return r.InitialSec + ((over+inc-1)/inc)*inc
}

// Amount is the charge for an answered call of the given duration, rounded
// half up to a micro-unit. Calls inside the grace period cost nothing.
func (r Rate) Amount(duration int) (billedSec int, amount int64) {
	billedSec, _, amount = r.AmountAfter(duration, 0)
	return billedSec, amount
}

// AmountAfter is the charge for a call whose first free billed seconds are
// paid for by a bundle of minutes: how many seconds are billed, how many of
// them the bundle takes, and what the rest costs. A call the bundle covers
// whole costs nothing, connection fee included; one it covers in part pays
// the fee and the remaining seconds.
func (r Rate) AmountAfter(duration int, free int64) (billedSec, freeSec int, amount int64) {
	billedSec = r.BilledSeconds(duration)
	if billedSec == 0 {
		return 0, 0, 0
	}
	freeSec = int(min(max(free, 0), int64(billedSec)))
	if freeSec == billedSec {
		return billedSec, freeSec, 0
	}
	return billedSec, freeSec, r.ConnectFee + (int64(billedSec-freeSec)*r.PricePerMin+30)/60
}

// Free reports whether calls at this rate cost nothing: such calls take
// nothing from a bundle either.
func (r Rate) Free() bool { return r.PricePerMin == 0 && r.ConnectFee == 0 }

// MaxSeconds is the longest call the given funds can pay for, capped at limit.
func (r Rate) MaxSeconds(funds int64, limit int) int {
	return r.MaxSecondsAfter(funds, 0, limit)
}

// MaxSecondsAfter is the longest call that free bundle seconds and then the
// given funds can pay for, capped at limit.
func (r Rate) MaxSecondsAfter(funds, free int64, limit int) int {
	free = max(free, 0)
	sec := free
	switch funds -= r.ConnectFee; {
	case funds <= 0:
	case r.PricePerMin == 0:
		return limit
	case funds/r.PricePerMin >= int64(limit): // also keeps the product below from overflowing
		return limit
	default:
		sec += funds * 60 / r.PricePerMin
	}
	if sec > int64(limit) {
		return limit
	}
	inc := int64(r.IncrementSec)
	if inc > 1 && sec > int64(r.InitialSec) {
		sec = int64(r.InitialSec) + (sec-int64(r.InitialSec))/inc*inc
	}
	return int(sec)
}

// Period names the bundle period a call starting at at belongs to: the
// calendar month in loc (nil = UTC), as "2006-01". A call belongs to the
// period it starts in, however long it lasts.
func Period(at time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return at.In(loc).Format("2006-01")
}

// Convert turns an amount in one currency into another at a rate given in
// micro-units of the target per unit of the source, rounded half up. A rate
// of zero or Micro leaves the amount as it is. The product is carried in
// 128 bits, so no amount that fits an int64 overflows on the way.
func Convert(amount, rate int64) int64 {
	if rate <= 0 || rate == Micro || amount == 0 {
		return amount
	}
	neg := amount < 0
	if neg {
		amount = -amount
	}
	hi, lo := bits.Mul64(uint64(amount), uint64(rate))
	lo, carry := bits.Add64(lo, Micro/2, 0)
	hi += carry
	if hi >= Micro {
		return 0 // beyond any real sum of money; never a wrong small number
	}
	q, _ := bits.Div64(hi, lo, Micro)
	if q > 1<<63-1 {
		return 0
	}
	if neg {
		return -int64(q)
	}
	return int64(q)
}

// ParseMoney converts a decimal string such as "0.0125" to micro-units.
func ParseMoney(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" || len(frac) > 6 || len(whole) > 12 {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	frac += strings.Repeat("0", 6-len(frac))
	var v int64
	for _, c := range whole + frac {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid amount %q", s)
		}
		v = v*10 + int64(c-'0')
	}
	if neg {
		v = -v
	}
	return v, nil
}

// LongestPrefix returns the rate with the longest prefix matching number.
func LongestPrefix[T any](byPrefix map[string]T, number string) (T, string, bool) {
	for i := len(number); i >= 0; i-- {
		if v, ok := byPrefix[number[:i]]; ok {
			return v, number[:i], true
		}
	}
	var zero T
	return zero, "", false
}
