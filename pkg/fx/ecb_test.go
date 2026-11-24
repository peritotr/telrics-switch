// SPDX-License-Identifier: AGPL-3.0-only

package fx

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) Table {
	t.Helper()
	f, err := os.Open("testdata/eurofxref-daily.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tab, err := ParseECB(f)
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestParseECB(t *testing.T) {
	tab := fixture(t)
	if want := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC); !tab.Published.Equal(want) {
		t.Errorf("published %s, want %s", tab.Published, want)
	}
	if len(tab.PerEUR) != 10 || tab.PerEUR["USD"] != "1.0939" || tab.PerEUR["ISK"] != "137.20" || tab.PerEUR["GBP"] != "0.83673" {
		t.Errorf("rates: %v", tab.PerEUR)
	}
}

func TestParseECBRefuses(t *testing.T) {
	doc := func(days string) string {
		return `<?xml version="1.0"?><gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><Cube>` + days + `</Cube></gesmes:Envelope>`
	}
	day := func(rates string) string { return `<Cube time='2026-11-20'>` + rates + `</Cube>` }
	for name, body := range map[string]string{
		"not xml":         "<html>maintenance",
		"no day":          doc(""),
		"two days":        doc(day(`<Cube currency='USD' rate='1.1'/>`) + `<Cube time='2026-11-19'><Cube currency='USD' rate='1.1'/></Cube>`),
		"bad date":        doc(`<Cube time='20.11.2026'><Cube currency='USD' rate='1.1'/></Cube>`),
		"no rates":        doc(day("")),
		"comma":           doc(day(`<Cube currency='USD' rate='1,0939'/>`)),
		"exponent":        doc(day(`<Cube currency='USD' rate='1e3'/>`)),
		"negative":        doc(day(`<Cube currency='USD' rate='-1.1'/>`)),
		"zero":            doc(day(`<Cube currency='USD' rate='0.000'/>`)),
		"empty rate":      doc(day(`<Cube currency='USD' rate=''/>`)),
		"too many digits": doc(day(`<Cube currency='USD' rate='1.0000000000001'/>`)),
		"lower case code": doc(day(`<Cube currency='usd' rate='1.1'/>`)),
		"euro itself":     doc(day(`<Cube currency='EUR' rate='1'/>`)),
		"twice":           doc(day(`<Cube currency='USD' rate='1.1'/><Cube currency='USD' rate='1.2'/>`)),
	} {
		if _, err := ParseECB(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRate(t *testing.T) {
	tab := fixture(t)
	for _, c := range []struct {
		cur, account string
		want         int64
	}{
		{"USD", "EUR", 914_160},     // 1 / 1.0939
		{"EUR", "TRY", 55_340_500},  // published as is
		{"USD", "TRY", 50_590_091},  // 55.3405 / 1.0939 = 50.5900905…, rounded up
		{"IDR", "EUR", 50},          // 1 / 19932.94: a small currency keeps two digits
		{"JPY", "GBP", 4_809},       // 0.83673 / 174.01
		{"CHF", "HUF", 405_268_887}, // 372.28 / 0.9186
		{"GBP", "USD", 1_307_351},   // 1.0939 / 0.83673
		{"TRY", "TRY", 1_000_000},   // the account's own currency
		{"EUR", "EUR", 1_000_000},
	} {
		got, err := tab.Rate(c.cur, c.account)
		if err != nil || got != c.want {
			t.Errorf("%s in %s: %d, %v; want %d", c.cur, c.account, got, err, c.want)
		}
	}
	if _, err := tab.Rate("XYZ", "EUR"); !errors.Is(err, ErrMissing) {
		t.Errorf("unpublished currency: %v", err)
	}
	if _, err := tab.Rate("USD", "XYZ"); !errors.Is(err, ErrMissing) {
		t.Errorf("unpublished account currency: %v", err)
	}
}

func TestRateRounding(t *testing.T) {
	tab := Table{PerEUR: map[string]string{"AAA": "400000", "BBB": "4000000", "CCC": "3", "BIG": "999999999999", "TNY": "0.000000000001"}}
	for _, c := range []struct {
		cur, account string
		want         int64
	}{
		{"AAA", "EUR", 3},       // 2.5 micro-units: halves go up
		{"CCC", "EUR", 333_333}, // 333 333.33…: down
	} {
		if got, err := tab.Rate(c.cur, c.account); err != nil || got != c.want {
			t.Errorf("%s in %s: %d, %v; want %d", c.cur, c.account, got, err, c.want)
		}
	}
	if got, err := tab.Rate("BBB", "EUR"); err == nil {
		t.Errorf("a rate below one micro-unit gave %d", got)
	}
	if got, err := tab.Rate("TNY", "BIG"); err == nil {
		t.Errorf("a rate beyond int64 gave %d", got)
	}
}
