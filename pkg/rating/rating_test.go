// SPDX-License-Identifier: AGPL-3.0-only

package rating

import (
	"testing"
	"time"
)

func TestBilledSeconds(t *testing.T) {
	cases := []struct {
		r    Rate
		dur  int
		want int
	}{
		{Rate{InitialSec: 60, IncrementSec: 60}, 1, 60},
		{Rate{InitialSec: 60, IncrementSec: 60}, 61, 120},
		{Rate{InitialSec: 60, IncrementSec: 1}, 61, 61},
		{Rate{InitialSec: 30, IncrementSec: 6}, 31, 36},
		{Rate{InitialSec: 1, IncrementSec: 1}, 0, 0},
		{Rate{InitialSec: 60, IncrementSec: 60, GraceSec: 3}, 3, 0},
		{Rate{InitialSec: 60, IncrementSec: 60, GraceSec: 3}, 4, 60},
	}
	for _, c := range cases {
		if got := c.r.BilledSeconds(c.dur); got != c.want {
			t.Errorf("%+v dur %d: got %d want %d", c.r, c.dur, got, c.want)
		}
	}
}

func TestAmount(t *testing.T) {
	r := Rate{PricePerMin: 12_500, ConnectFee: 1_000, InitialSec: 1, IncrementSec: 1}
	if _, a := r.Amount(90); a != 1_000+18_750 {
		t.Errorf("got %d", a)
	}
	// 1 second at 0.0125/min = 208.33 micro, rounds to 208.
	if _, a := r.Amount(1); a != 1_000+208 {
		t.Errorf("got %d", a)
	}
	if _, a := r.Amount(0); a != 0 {
		t.Errorf("unanswered call charged %d", a)
	}
}

func TestMaxSecondsNeverOverdraws(t *testing.T) {
	r := Rate{PricePerMin: 12_500, ConnectFee: 1_000, InitialSec: 60, IncrementSec: 60}
	for funds := int64(0); funds < 200_000; funds += 777 {
		sec := r.MaxSeconds(funds, 7200)
		if _, a := r.Amount(sec); sec > 60 && a > funds {
			t.Fatalf("funds %d allows %d s costing %d", funds, sec, a)
		}
	}
	if got := (Rate{}).MaxSeconds(1, 7200); got != 7200 {
		t.Errorf("free route: got %d", got)
	}
}

func TestParseMoney(t *testing.T) {
	ok := map[string]int64{"0.0125": 12_500, "1": 1_000_000, "-2.5": -2_500_000, ".5": 500_000, "10.000001": 10_000_001}
	for in, want := range ok {
		if got, err := ParseMoney(in); err != nil || got != want {
			t.Errorf("%q: got %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "abc", "1.2345678", "1,5", "."} {
		if _, err := ParseMoney(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestLongestPrefix(t *testing.T) {
	m := map[string]int{"": 0, "90": 1, "9053": 2}
	if v, p, _ := LongestPrefix(m, "905321112233"); v != 2 || p != "9053" {
		t.Errorf("got %d %q", v, p)
	}
	if v, _, _ := LongestPrefix(m, "4420"); v != 0 {
		t.Errorf("default route: got %d", v)
	}
	if _, _, ok := LongestPrefix(map[string]int{"90": 1}, "44"); ok {
		t.Error("expected no match")
	}
}

func TestWindow(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse("Mon 2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	const weekdays, weekend, friday = 31, 96, 16
	for _, c := range []struct {
		w    Window
		when string
		want bool
	}{
		{Window{}, "Sun 2026-11-22 03:00", true},
		{Window{Days: EveryDay}, "Sun 2026-11-22 03:00", true},
		{Window{Days: weekdays}, "Fri 2026-11-20 23:59", true},
		{Window{Days: weekdays}, "Sat 2026-11-21 00:00", false},
		{Window{Days: weekend}, "Sun 2026-11-22 12:00", true},
		{Window{From: 8 * 60, To: 18 * 60}, "Mon 2026-11-23 08:00", true},
		{Window{From: 8 * 60, To: 18 * 60}, "Mon 2026-11-23 07:59", false},
		{Window{From: 8 * 60, To: 18 * 60}, "Mon 2026-11-23 18:00", false}, // ends just before
		{Window{From: 22 * 60, To: 6 * 60}, "Mon 2026-11-23 22:00", true},
		{Window{From: 22 * 60, To: 6 * 60}, "Mon 2026-11-23 05:59", true},
		{Window{From: 22 * 60, To: 6 * 60}, "Mon 2026-11-23 06:00", false},
		{Window{From: 22 * 60, To: 6 * 60}, "Mon 2026-11-23 12:00", false},
		// Across midnight the window belongs to the day it starts on.
		{Window{Days: friday, From: 22 * 60, To: 6 * 60}, "Fri 2026-11-20 23:00", true},
		{Window{Days: friday, From: 22 * 60, To: 6 * 60}, "Sat 2026-11-21 02:00", true},
		{Window{Days: friday, From: 22 * 60, To: 6 * 60}, "Fri 2026-11-20 02:00", false},
		{Window{Days: friday, From: 22 * 60, To: 6 * 60}, "Sat 2026-11-21 23:00", false},
	} {
		if got := c.w.Covers(at(c.when), nil); got != c.want {
			t.Errorf("%+v at %s: got %v", c.w, c.when, got)
		}
	}
	if (Window{}).Limited() || (Window{Days: EveryDay, From: 60, To: 60}).Limited() || !(Window{Days: 1}).Limited() || !(Window{To: 60}).Limited() {
		t.Error("Limited")
	}
}

func TestSelect(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	night := Window{From: 22 * 60, To: 6 * 60}
	list := Tidy([]Rate{
		{ID: 1, PricePerMin: 100, EffectiveFrom: day(1)},
		{ID: 2, PricePerMin: 50, Window: night, EffectiveFrom: day(1)},
		{ID: 3, PricePerMin: 40, Window: night, EffectiveFrom: day(5)},            // replaces 2
		{ID: 4, PricePerMin: 90, EffectiveFrom: day(3)},                           // replaces 1
		{ID: 5, PricePerMin: 30, Window: Window{Days: 64}, EffectiveFrom: day(2)}, // Sundays
	})
	if len(list) != 3 {
		t.Fatalf("tidied: %+v", list)
	}
	for when, want := range map[string]int64{
		"2026-11-23T12:00:00Z": 90, // Monday noon
		"2026-11-23T23:00:00Z": 40, // Monday night
		"2026-11-22T12:00:00Z": 30, // Sunday noon
		"2026-11-22T23:00:00Z": 40, // Sunday night: both cover it, the later rate wins
	} {
		at, _ := time.Parse(time.RFC3339, when)
		if r, ok := Select(list, at, nil); !ok || r.PricePerMin != want {
			t.Errorf("%s: got %d %v, want %d", when, r.PricePerMin, ok, want)
		}
	}
	// Only limited rates, none covering the moment: no rate at this prefix.
	if _, ok := Select(Tidy([]Rate{{Window: night}}), time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), nil); ok {
		t.Error("a rate outside its window was selected")
	}
}

func TestHolidays(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	// Wednesday 15 July 2026 counts as a Sunday, Thursday 16 July as a
	// Saturday; read in Istanbul time.
	cal := Calendar{{2026, time.July, 15}: time.Sunday, {2026, time.July, 16}: time.Saturday}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, ist)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	const weekdays, saturday, sunday, wednesday = 31, 32, 64, 4
	for _, c := range []struct {
		w    Window
		when string
		cal  Calendar
		want bool
	}{
		{Window{Days: sunday}, "2026-07-15 12:00", cal, true},     // the holiday is a Sunday
		{Window{Days: weekdays}, "2026-07-15 12:00", cal, false},  // and so not a weekday
		{Window{Days: wednesday}, "2026-07-15 12:00", cal, false}, // nor its own weekday
		{Window{Days: wednesday}, "2026-07-15 12:00", nil, true},  // without a calendar it is
		{Window{Days: saturday}, "2026-07-16 09:00", cal, true},   // a chosen day
		{Window{Days: sunday}, "2026-07-16 09:00", cal, false},    //
		{Window{Days: weekdays}, "2026-07-14 23:59", cal, true},   // the day before is untouched
		{Window{Days: sunday}, "2026-07-15 00:00", cal, true},     // from midnight on
		{Window{Days: weekdays}, "2026-07-17 00:00", cal, true},   // and to midnight
		{Window{Days: EveryDay}, "2026-07-15 12:00", cal, true},   //
		{Window{Days: sunday, From: 8 * 60, To: 18 * 60}, "2026-07-15 07:59", cal, false},
		{Window{Days: sunday, From: 8 * 60, To: 18 * 60}, "2026-07-15 08:00", cal, true},
		// Across midnight the window belongs to the day it starts on: the
		// night after the holiday is a Sunday night, the night before not.
		{Window{Days: sunday, From: 22 * 60, To: 6 * 60}, "2026-07-16 02:00", cal, true},
		{Window{Days: sunday, From: 22 * 60, To: 6 * 60}, "2026-07-15 02:00", cal, false},
		{Window{Days: weekdays, From: 22 * 60, To: 6 * 60}, "2026-07-15 02:00", cal, true},
		{Window{Days: weekdays, From: 22 * 60, To: 6 * 60}, "2026-07-16 02:00", cal, false},
	} {
		if got := c.w.Covers(at(c.when), c.cal); got != c.want {
			t.Errorf("%+v at %s (calendar %v): got %v", c.w, c.when, c.cal != nil, got)
		}
	}
	// The date is the one in the window's time zone: 21:30 UTC on the 14th
	// is already the holiday in Istanbul, 21:30 UTC on the 15th is over it.
	for when, want := range map[string]bool{
		"2026-07-14T20:59:00Z": false, "2026-07-14T21:00:00Z": true,
		"2026-07-15T20:59:00Z": true, "2026-07-15T21:00:00Z": false,
	} {
		u, _ := time.Parse(time.RFC3339, when)
		if got := (Window{Days: sunday}).Covers(u.In(ist), Calendar{{2026, time.July, 15}: time.Sunday}); got != want {
			t.Errorf("Sunday window at %s (Istanbul): got %v", when, got)
		}
		if got := (Window{Days: sunday}).Covers(u, Calendar{{2026, time.July, 15}: time.Sunday}); got != (when[:10] == "2026-07-15") {
			t.Errorf("Sunday window at %s (UTC): got %v", when, got)
		}
	}
	// Select prices a holiday at the Sunday rate, a working day as before.
	list := Tidy([]Rate{
		{ID: 1, PricePerMin: 100},
		{ID: 2, PricePerMin: 30, Window: Window{Days: sunday}},
	})
	for when, want := range map[string]int64{"2026-07-15 12:00": 30, "2026-07-14 12:00": 100, "2026-07-19 12:00": 30} {
		if r, ok := Select(list, at(when), cal); !ok || r.PricePerMin != want {
			t.Errorf("%s: got %d %v, want %d", when, r.PricePerMin, ok, want)
		}
	}
	if r, _ := Select(list, at("2026-07-15 12:00"), nil); r.PricePerMin != 100 {
		t.Errorf("no calendar: got %d", r.PricePerMin)
	}
}

func BenchmarkSelect(b *testing.B) {
	list := Tidy([]Rate{
		{ID: 1, PricePerMin: 100},
		{ID: 2, PricePerMin: 50, Window: Window{From: 22 * 60, To: 6 * 60}},
		{ID: 3, PricePerMin: 30, Window: Window{Days: 96}},
	})
	at := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	cal := Calendar{{2026, time.July, 14}: time.Sunday, {2026, time.December, 25}: time.Sunday}
	b.Run("no-calendar", func(b *testing.B) {
		for range b.N {
			Select(list, at, nil)
		}
	})
	b.Run("calendar", func(b *testing.B) {
		for range b.N {
			Select(list, at, cal)
		}
	})
}

func TestConvert(t *testing.T) {
	for _, c := range []struct{ amount, rate, want int64 }{
		{1_000_000, 0, 1_000_000}, // no rate: the same currency
		{1_000_000, Micro, 1_000_000},
		{1_000_000, 1_200_000, 1_200_000}, // 1 EUR = 1.20
		{10_000, 1_200_000, 12_000},
		{1, 500_000, 1}, // half a micro-unit rounds up
		{1, 499_999, 0},
		{-10_000, 1_200_000, -12_000},
		{3, 333_333, 1},
		{1_000_000, 30_000, 30_000},               // 1 TRY = 0.03
		{9_000_000_000_000_000_000, 2_000_000, 0}, // would not fit: nothing rather than nonsense
		{4_000_000_000_000_000, 2_000_000, 8_000_000_000_000_000},
		{0, 1_200_000, 0},
	} {
		if got := Convert(c.amount, c.rate); got != c.want {
			t.Errorf("Convert(%d, %d) = %d, want %d", c.amount, c.rate, got, c.want)
		}
	}
}

func TestAmountAfterBundle(t *testing.T) {
	r := Rate{PricePerMin: 60_000, ConnectFee: 5_000, InitialSec: 30, IncrementSec: 6}
	for _, c := range []struct {
		duration     int
		free         int64
		billed, used int
		amount       int64
	}{
		{0, 100, 0, 0, 0},                // unanswered: nothing billed, nothing taken
		{10, 100, 30, 30, 0},             // covered whole: no fee either
		{31, 36, 36, 36, 0},              // exactly the seconds left
		{31, 20, 36, 20, 5_000 + 16_000}, // the bundle ends inside the call
		{31, 0, 36, 0, 5_000 + 36_000},
		{31, -5, 36, 0, 5_000 + 36_000}, // an overdrawn count gives nothing
	} {
		billed, used, amount := r.AmountAfter(c.duration, c.free)
		if billed != c.billed || used != c.used || amount != c.amount {
			t.Errorf("AmountAfter(%d, %d) = %d, %d, %d; want %d, %d, %d", c.duration, c.free, billed, used, amount, c.billed, c.used, c.amount)
		}
	}
	// Without a bundle the two agree.
	for d := 0; d < 200; d += 7 {
		b1, a1 := r.Amount(d)
		b2, _, a2 := r.AmountAfter(d, 0)
		if b1 != b2 || a1 != a2 {
			t.Fatalf("duration %d: %d/%d against %d/%d", d, b1, a1, b2, a2)
		}
	}
	if (Rate{}).Free() != true || r.Free() || (Rate{ConnectFee: 1}).Free() {
		t.Error("Free")
	}
}

func TestMaxSecondsAfterBundleNeverOverdraws(t *testing.T) {
	r := Rate{PricePerMin: 12_500, ConnectFee: 1_000, InitialSec: 60, IncrementSec: 60}
	for free := int64(0); free < 400; free += 37 {
		for funds := int64(0); funds < 100_000; funds += 777 {
			sec := r.MaxSecondsAfter(funds, free, 7200)
			if _, _, a := r.AmountAfter(sec, free); sec > 60 && a > funds {
				t.Fatalf("funds %d and %d free s allow %d s costing %d", funds, free, sec, a)
			}
			if sec < r.MaxSeconds(funds, 7200) {
				t.Fatalf("a bundle of %d s shortened the call: %d", free, sec)
			}
		}
	}
	second := Rate{PricePerMin: 60_000, InitialSec: 1, IncrementSec: 1}
	for _, c := range []struct {
		funds, free int64
		want        int
	}{{0, 0, 0}, {0, 90, 90}, {30_000, 90, 120}, {30_000, -10, 30}, {1 << 60, 90, 7200}, {0, 9000, 7200}} {
		if got := second.MaxSecondsAfter(c.funds, c.free, 7200); got != c.want {
			t.Errorf("MaxSecondsAfter(%d, %d) = %d, want %d", c.funds, c.free, got, c.want)
		}
	}
}

func TestPeriod(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 31, 21, 30, 0, 0, time.UTC) // already February in Istanbul
	if got := Period(at, nil); got != "2026-01" {
		t.Errorf("UTC: %s", got)
	}
	if got := Period(at, ist); got != "2026-02" {
		t.Errorf("Istanbul: %s", got)
	}
}
