// SPDX-License-Identifier: AGPL-3.0-only

// Package fx reads published reference rates of exchange and turns them
// into the account's rates: micro-units of the account's currency per unit
// of another (T-20, ADR-0036). Every step is exact integer arithmetic; the
// published decimals are never read as floating point.
package fx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"

	"softswitch/core/pkg/rating"
)

// Base is the currency the European Central Bank quotes against.
const Base = "EUR"

// ECBURL is the European Central Bank's daily reference rates: free, no key.
const ECBURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

// Table is one day's published rates: how many units of each currency one
// euro buys, as the decimal text it was published in.
type Table struct {
	Published time.Time         // the day the rates are for (UTC midnight)
	PerEUR    map[string]string // currency -> decimal, e.g. "USD" -> "1.0876"
}

var (
	codeRe    = regexp.MustCompile(`^[A-Z]{3}$`)
	decimalRe = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,12})?$`)
)

// ParseECB reads the European Central Bank's daily file
// (eurofxref-daily.xml). A file without a date or without a single rate is
// an error, as is any rate that is not a plain positive decimal: a wrong
// file must never turn into wrong rates.
func ParseECB(r io.Reader) (Table, error) {
	var doc struct {
		Days []struct {
			Time  string `xml:"time,attr"`
			Rates []struct {
				Currency string `xml:"currency,attr"`
				Rate     string `xml:"rate,attr"`
			} `xml:"Cube"`
		} `xml:"Cube>Cube"`
	}
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return Table{}, fmt.Errorf("reading the reference rates: %w", err)
	}
	if len(doc.Days) != 1 {
		return Table{}, fmt.Errorf("reading the reference rates: %d days in the file, want one", len(doc.Days))
	}
	day := doc.Days[0]
	published, err := time.Parse(time.DateOnly, day.Time)
	if err != nil {
		return Table{}, fmt.Errorf("reading the reference rates: date %q: %w", day.Time, err)
	}
	t := Table{Published: published, PerEUR: map[string]string{}}
	for _, r := range day.Rates {
		cur, rate := strings.TrimSpace(r.Currency), strings.TrimSpace(r.Rate)
		if !codeRe.MatchString(cur) || cur == Base {
			return Table{}, fmt.Errorf("reading the reference rates: currency %q", r.Currency)
		}
		if _, err := parseDecimal(rate); err != nil {
			return Table{}, fmt.Errorf("reading the reference rates: %s: %w", cur, err)
		}
		if _, dup := t.PerEUR[cur]; dup {
			return Table{}, fmt.Errorf("reading the reference rates: %s twice", cur)
		}
		t.PerEUR[cur] = rate
	}
	if len(t.PerEUR) == 0 {
		return Table{}, errors.New("reading the reference rates: no rates in the file")
	}
	return t, nil
}

// parseDecimal reads a plain positive decimal exactly, as a fraction.
func parseDecimal(s string) (*big.Rat, error) {
	if !decimalRe.MatchString(s) {
		return nil, fmt.Errorf("rate %q is not a plain decimal", s)
	}
	whole, frac, _ := strings.Cut(s, ".")
	num, _ := new(big.Int).SetString(whole+frac, 10)
	if num.Sign() <= 0 {
		return nil, fmt.Errorf("rate %q is not above zero", s)
	}
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(frac))), nil)
	return new(big.Rat).SetFrac(num, den), nil
}

// ErrMissing says the table has no rate for a currency.
var ErrMissing = errors.New("not in the published rates")

// Rate is what one unit of cur is worth in account, in micro-units of
// account: the table's rates crossed through the euro (account per euro
// divided by cur per euro), exactly, then rounded to the nearest
// micro-unit, halves up. A rate that would round to zero, or not fit an
// int64, is an error rather than a wrong number.
func (t Table) Rate(cur, account string) (int64, error) {
	per := func(c string) (*big.Rat, error) {
		if c == Base {
			return big.NewRat(1, 1), nil
		}
		s, ok := t.PerEUR[c]
		if !ok {
			return nil, fmt.Errorf("%s: %w", c, ErrMissing)
		}
		return parseDecimal(s)
	}
	from, err := per(cur)
	if err != nil {
		return 0, err
	}
	to, err := per(account)
	if err != nil {
		return 0, err
	}
	// micro = round(to / from * Micro) = floor((2·to.num·from.den·Micro + d) / 2d), d = to.den·from.num
	n := new(big.Int).Mul(to.Num(), from.Denom())
	n.Mul(n, big.NewInt(2*rating.Micro))
	d := new(big.Int).Mul(to.Denom(), from.Num())
	n.Add(n, d)
	n.Quo(n, d.Mul(d, big.NewInt(2)))
	if !n.IsInt64() {
		return 0, fmt.Errorf("%s in %s: rate too large", cur, account)
	}
	if n.Sign() <= 0 {
		return 0, fmt.Errorf("%s in %s: rate below one micro-unit", cur, account)
	}
	return n.Int64(), nil
}
