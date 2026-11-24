// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"testing"
	"time"
)

func TestReportPeriod(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		kind, now, start, end string
		loc                   *time.Location
	}{
		// Sunday 22 November 2026, 14:00 UTC.
		{"daily", "2026-11-22T14:00:00Z", "2026-11-21T00:00:00Z", "2026-11-22T00:00:00Z", time.UTC},
		{"weekly", "2026-11-22T14:00:00Z", "2026-11-09T00:00:00Z", "2026-11-16T00:00:00Z", time.UTC},
		{"monthly", "2026-11-22T14:00:00Z", "2026-10-01T00:00:00Z", "2026-11-01T00:00:00Z", time.UTC},
		// A Monday: the week that ended last midnight.
		{"weekly", "2026-11-23T00:30:00Z", "2026-11-16T00:00:00Z", "2026-11-23T00:00:00Z", time.UTC},
		// 22:30 UTC on Sunday is already Monday in Istanbul.
		{"daily", "2026-11-22T22:30:00Z", "2026-11-21T21:00:00Z", "2026-11-22T21:00:00Z", ist},
		{"weekly", "2026-11-22T22:30:00Z", "2026-11-15T21:00:00Z", "2026-11-22T21:00:00Z", ist},
		// January: the month before is in the year before.
		{"monthly", "2027-01-01T06:00:00Z", "2026-12-01T00:00:00Z", "2027-01-01T00:00:00Z", time.UTC},
	} {
		start, end := reportPeriod(c.kind, at(c.now), c.loc)
		if !start.Equal(at(c.start)) || !end.Equal(at(c.end)) {
			t.Errorf("%s at %s in %s: %s to %s, want %s to %s", c.kind, c.now, c.loc, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), c.start, c.end)
		}
	}
}
