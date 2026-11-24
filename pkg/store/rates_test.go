// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/pkg/routing"
)

// testDB is an empty, migrated test database; the test wipes it, so only a
// database whose name ends in _test is taken (make test-api).
func testDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if !strings.HasSuffix(strings.SplitN(url, "?", 2)[0], "_test") {
		t.Fatalf("TEST_DATABASE_URL must point at a database whose name ends in _test")
	}
	ctx := context.Background()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, q := range []string{`drop schema public cascade`, `create schema public`} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

// sameRates fails the test unless both snapshots hold the same rates.
func sameRates(t *testing.T, step string, got, want *routing.Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(got.Tariffs, want.Tariffs) {
		t.Errorf("%s: single rates differ:\n got %v\nwant %v", step, got.Tariffs, want.Tariffs)
	}
	if len(got.Timed) != len(want.Timed) {
		t.Errorf("%s: %d tariffs with timed rates, want %d", step, len(got.Timed), len(want.Timed))
	}
	for id, w := range want.Timed {
		g, ok := got.Timed[id]
		if !ok || g.Loc.String() != w.Loc.String() || !reflect.DeepEqual(g.Cal, w.Cal) || !reflect.DeepEqual(g.Rates, w.Rates) {
			t.Errorf("%s: timed rates of tariff %d:\n got %+v\nwant %+v", step, id, g, w)
		}
	}
}

// TestIncrementalRates checks that a load which reads only the changed
// tariffs builds the same rates as one that reads them all, whatever wrote
// the change, and that it did read only those.
func TestIncrementalRates(t *testing.T) {
	ctx, pool := testDB(t)
	defer SetFullReload(FullReloadEvery())
	SetFullReload(time.Hour)
	rates.drop()

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// check loads incrementally and in full and compares; whole and
	// prefixes are what the incremental load must have read (whole -1:
	// everything).
	check := func(step string, whole, prefixes int) {
		t.Helper()
		got, err := LoadSnapshot(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		l := LastRateLoad()
		want, err := loadSnapshot(ctx, pool, false)
		if err != nil {
			t.Fatal(err)
		}
		sameRates(t, step, got, want)
		if whole < 0 && !l.Full || whole >= 0 && (l.Full || l.Tariffs != whole || l.Prefixes != prefixes) {
			t.Errorf("%s: load %+v, want %d tariffs and %d prefixes read", step, l, whole, prefixes)
		}
	}

	exec(`insert into resellers (name) values ('root')`)
	exec(`insert into tariffs (reseller_id, name, kind, timezone) values
		(1, 'a', 'sell', 'Europe/Istanbul'), (1, 'b', 'sell', 'UTC'), (1, 'c', 'sell', 'UTC'), (1, 'd', 'buy', 'UTC')`)
	// a: one rate for all hours, one with a weekend rate as well, one
	// ending in two seconds and an old one; b: plain; c: none; d: only a
	// rate coming in force in two seconds.
	exec(`insert into rates (tariff_id, prefix, price_per_min, effective_from, effective_to) values
		(1, '90', 100, now() - interval '1 day', null),
		(1, '9021', 200, now() - interval '1 day', null),
		(1, '9053', 300, now() - interval '1 day', now() + interval '2 seconds'),
		(1, '9054', 400, now() - interval '9 days', now() - interval '1 day'),
		(2, '44', 500, now() - interval '1 day', null),
		(4, '1', 600, now() + interval '2 seconds', null)`)
	exec(`insert into rates (tariff_id, prefix, price_per_min, days, effective_from) values (1, '9021', 150, 96, now() - interval '1 day')`)
	exec(`insert into holidays (reseller_id, day, counts_as) values (1, '2026-12-25', 7)`)

	check("first load", -1, 0)
	check("nothing changed", 0, 0)

	exec(`insert into rates (tariff_id, prefix, price_per_min) values (1, '9055', 700)`)
	check("a rate added", 0, 1)
	exec(`update rates set price_per_min = 250 where tariff_id = 1 and prefix = '9021' and days = 127`)
	check("a rate changed", 0, 1)
	exec(`delete from rates where tariff_id = 2`)
	check("the only rate of a tariff deleted", 0, 1)
	exec(`update rates set tariff_id = 2 where tariff_id = 1 and prefix = '9055'`)
	check("a rate moved to another tariff", 0, 2)
	exec(`update rates set prefix = '9056' where prefix = '9055'`)
	check("a prefix changed", 0, 2)
	exec(`update tariffs set timezone = 'Asia/Tokyo' where id = 1`)
	check("a time zone changed", 0, 0)
	exec(`insert into holidays (reseller_id, day, counts_as) values (1, '2026-12-26', 7)`)
	check("a holiday added", 0, 0)
	// Several writes in one transaction count once.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`insert into rates (tariff_id, prefix, price_per_min) values (3, '33', 800)`,
		`insert into rates (tariff_id, prefix, price_per_min, days, time_from, time_to) values (3, '33', 90, 127, 1200, 480)`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	check("a write not yet committed", 0, 0)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	check("the write committed", 0, 1)
	// A statement of many rows notes the tariff only.
	exec(`insert into rates (tariff_id, prefix, price_per_min) select 3, (700000 + i)::text, i from generate_series(1, 10001) i`)
	check("many rates added", 1, 0)
	exec(`delete from rates where tariff_id = 3 and prefix like '7%'`)
	check("many rates deleted", 1, 0)

	// The rate of a that ends and the one of d that begins: no write at all.
	time.Sleep(2500 * time.Millisecond)
	check("rates came into and went out of force", 2, 0)
	check("nothing changed since", 0, 0)

	exec(`delete from tariffs where id = 4`)
	check("a tariff deleted", 0, 0)
	exec(`truncate rates`)
	check("every rate removed", 3, 0)

	// The periodic full load, and a cache dropped while the process stopped
	// loading.
	SetFullReload(50 * time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	check("time for a full load", -1, 0)
	SetFullReload(time.Hour)
	rates.drop()
	check("cache dropped", -1, 0)
	SetFullReload(0)
	check("cache off", -1, 0)
	check("cache off, again", -1, 0)
}
