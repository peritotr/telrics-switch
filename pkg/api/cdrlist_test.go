// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"errors"
	"net/url"
	"testing"
)

func TestArchivePage(t *testing.T) {
	counted := 0
	total := func(n int) func() (int, error) {
		return func() (int, error) { counted++; return n, nil }
	}
	for _, c := range []struct {
		name                  string
		offset, limit, got, n int
		wantOff, wantLim      int
		counts                bool
	}{
		{"page full from PostgreSQL", 0, 50, 50, 500, 0, 0, false},
		{"page ends in the archive", 40, 50, 10, 50, 0, 40, false},
		{"page starts in the archive", 120, 50, 0, 100, 20, 50, true},
		{"page starts right at the archive", 100, 50, 0, 100, 0, 50, true},
		{"nothing kept in PostgreSQL", 30, 10, 0, 0, 30, 10, true},
		{"empty page asked for", 0, 0, 0, 0, 0, 0, false},
	} {
		counted = 0
		off, lim, err := archivePage(c.offset, c.limit, c.got, total(c.n))
		if err != nil || off != c.wantOff || lim != c.wantLim || (counted > 0) != c.counts {
			t.Errorf("%s: got offset %d limit %d (counted %d, %v), want %d %d", c.name, off, lim, counted, err, c.wantOff, c.wantLim)
		}
	}
	if _, _, err := archivePage(10, 10, 0, func() (int, error) { return 0, errors.New("down") }); err == nil {
		t.Error("a failed count was not reported")
	}
}

func TestParseCDRQuery(t *testing.T) {
	res := &Resource{Filters: []string{"client_id", "sales_invoice_id"}}
	q, err := parseCDRQuery(url.Values{"limit": {"5000"}, "offset": {"7"}, "from": {"2026-01-01T00:00:00Z"}, "dst": {"90"}}, res)
	if err != nil || q.limit != 1000 || q.offset != 7 || q.from == nil || q.none || len(q.where) != 2 || q.params["from"] != "2026-01-01 00:00:00.000000" {
		t.Errorf("parsed %+v, %v", q, err)
	}
	// An extension's filter: archived records have none of its values.
	if q, err := parseCDRQuery(url.Values{"sales_invoice_id": {"3"}}, res); err != nil || !q.none {
		t.Errorf("extension filter: %+v, %v", q, err)
	}
	if q, err := parseCDRQuery(url.Values{"client_id": {"abc"}}, res); err != nil || !q.none {
		t.Errorf("a number filter that is no number: %+v, %v", q, err)
	}
	for _, bad := range []url.Values{{"bogus": {"1"}}, {"limit": {"-1"}}, {"to": {"yesterday"}}, {"sales_invoice_id": {"3"}}} {
		r := res
		if bad.Has("sales_invoice_id") {
			r = nil // without the resource the extension's filter is unknown
		}
		if _, err := parseCDRQuery(bad, r); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
