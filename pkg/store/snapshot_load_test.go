// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"maps"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/pkg/routing"
)

// TestSnapshotLoadSize measures how long a full snapshot load takes and how
// much memory it needs with a tariff of SNAPSHOT_RATES rates (5 million by
// default), as T-30 asks. It needs a database of its own:
// SNAPSHOT_DATABASE_URL (make bench-snapshot); the data set is built once and
// kept, so later runs only measure.
func TestSnapshotLoadSize(t *testing.T) {
	url := os.Getenv("SNAPSHOT_DATABASE_URL")
	if url == "" {
		t.Skip("SNAPSHOT_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	count := int64(5_000_000)
	if v := os.Getenv("SNAPSHOT_RATES"); v != "" {
		if count, err = strconv.ParseInt(v, 10, 64); err != nil {
			t.Fatal(err)
		}
	}
	seedSnapshotSize(t, ctx, pool, count)

	// The snapshot in use stays alive while the next is built, as in the
	// engine.
	var prev *routing.Snapshot
	measure := func(name string) {
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		var peak atomic.Uint64
		done := make(chan struct{})
		go func() {
			var m runtime.MemStats
			for {
				select {
				case <-done:
					return
				case <-time.After(20 * time.Millisecond):
				}
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}()
		start := time.Now()
		snap, err := LoadSnapshot(ctx, pool)
		took := time.Since(start)
		close(done)
		if err != nil {
			t.Fatal(err)
		}
		prev = snap
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		n := 0
		for _, m := range snap.Tariffs {
			n += len(m)
		}
		timed := 0
		for _, tt := range snap.Timed {
			timed += len(tt.Rates)
		}
		t.Logf("%s: %d single-rate prefixes, %d timed; load %v (rates %+v); heap in use before %d MiB, peak %d MiB, after %d MiB; allocated %d MiB",
			name, n, timed, took.Round(time.Millisecond), *LastRateLoad(), before.HeapInuse>>20, peak.Load()>>20, after.HeapInuse>>20,
			(after.TotalAlloc-before.TotalAlloc)>>20)
	}
	exec := func(q string) {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	defer SetFullReload(FullReloadEvery())
	SetFullReload(0)
	measure("full, nothing held")
	measure("full, previous held (every reload before T-30)")
	measure("full, previous held, again")
	SetFullReload(time.Hour)
	rates.drop()
	measure("first load, cache on")
	measure("nothing changed")
	exec(`update rates set price_per_min = price_per_min + 1 where tariff_id = 2`)
	measure("a small tariff changed")
	exec(`update rates set price_per_min = price_per_min + 1 where id = 1`)
	measure("one rate of the large tariff changed")
	measure("nothing changed")
	exec(`update rates set price_per_min = price_per_min + 1 where tariff_id = 1 and id <= 1000`)
	measure("a thousand rates of the large tariff changed")
	exec(`update rates set price_per_min = price_per_min + 1 where tariff_id = 1 and id <= 20000`)
	measure("20,000 rates of the large tariff changed in one statement")
	measure("nothing changed")
	if prev == nil {
		t.Fatal("nothing loaded")
	}
	start := time.Now()
	big := maps.Clone(prev.Tariffs[1])
	t.Logf("copying the large tariff's map of %d: %v", len(big), time.Since(start).Round(time.Millisecond))
}

// seedSnapshotSize builds the data set unless it is there: one tariff with
// rates in force, a twentieth of them with a weekend rate as well, and a
// tenth as many rates no longer in force.
func seedSnapshotSize(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rates int64) {
	t.Helper()
	var have int64
	if err := pool.QueryRow(ctx, `select count(*) from rates`).Scan(&have); err != nil {
		t.Fatal(err)
	}
	want := rates + rates/20 + rates/10 + 1000
	if have == want {
		return
	}
	start := time.Now()
	for _, q := range []string{
		`truncate resellers, tariffs, rates restart identity cascade`,
		`insert into resellers (name) values ('T-30 measurement')`,
		`insert into tariffs (reseller_id, name, kind, timezone) values (1, 'T-30 large', 'sell', 'Europe/Istanbul')`,
		// Ten country codes, numbers of seven digits and more under each:
		// prefixes of 8 to 10 digits, as a tariff priced by number range has.
		`insert into rates (tariff_id, prefix, price_per_min, connect_fee, initial_sec, increment_sec, effective_from)
		 select 1, (array['1','44','49','90','33','7','86','91','234','55'])[i % 10 + 1] || (1000000 + i / 10)::text,
		        1000 + (i * 7919) % 200000, case when i % 13 = 0 then 5000 else 0 end, 1, case when i % 3 = 0 then 60 else 1 end,
		        now() - interval '30 days'
		 from generate_series(0, $1::bigint - 1) i`,
		`insert into rates (tariff_id, prefix, price_per_min, days, time_from, time_to, effective_from)
		 select 1, (array['1','44','49','90','33','7','86','91','234','55'])[i % 10 + 1] || (1000000 + i / 10)::text,
		        500 + (i * 104729) % 100000, 96, 0, 0, now() - interval '30 days'
		 from generate_series(0, $1::bigint - 1, 20) i`,
		`insert into rates (tariff_id, prefix, price_per_min, effective_from, effective_to)
		 select 1, (array['1','44','49','90','33','7','86','91','234','55'])[i % 10 + 1] || (1000000 + i / 10)::text,
		        900 + (i * 31) % 150000, now() - interval '90 days', now() - interval '30 days'
		 from generate_series(0, $1::bigint - 1, 10) i`,
		`insert into tariffs (reseller_id, name, kind) values (1, 'T-30 small', 'sell')`,
		`insert into rates (tariff_id, prefix, price_per_min) select 2, (100 + i)::text, 1000 from generate_series(1, 1000) i`,
		`analyze rates`,
	} {
		args := []any{}
		if strings.Contains(q, "$1") {
			args = append(args, rates)
		}
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Logf("data set built in %v", time.Since(start).Round(time.Second))
}
