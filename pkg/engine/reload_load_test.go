// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"softswitch/core/pkg/routing"
	"softswitch/core/pkg/store"
)

// TestReloadUnderLoad measures what reloading the snapshot of a large tariff
// costs calls being decided meanwhile (T-30): decisions are made at
// RELOAD_LOAD_CPS (1,000) a second with their trip to Redis while the engine
// reloads every 10 seconds (a thousand rates changed before each), once reading every rate each time (as before
// T-30) and once reading only what changed. It needs the data set of
// store.TestSnapshotLoadSize (SNAPSHOT_DATABASE_URL) and the lab Redis
// (TEST_REDIS_URL; database 15, wiped): make bench-snapshot.
func TestReloadUnderLoad(t *testing.T) {
	url := os.Getenv("SNAPSHOT_DATABASE_URL")
	if url == "" || os.Getenv("TEST_REDIS_URL") == "" {
		t.Skip("SNAPSHOT_DATABASE_URL or TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cps := 1000
	if v := os.Getenv("RELOAD_LOAD_CPS"); v != "" {
		if cps, err = strconv.Atoi(v); err != nil {
			t.Fatal(err)
		}
	}
	defer store.SetFullReload(store.FullReloadEvery())
	for _, mode := range []struct {
		name string
		full time.Duration
	}{{"every rate on every reload", 0}, {"only what changed", time.Hour}} {
		store.SetFullReload(mode.full)
		reloadUnderLoad(t, ctx, pool, mode.name, cps)
	}
}

func reloadUnderLoad(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, cps int) {
	e, _, rdb := testEngine(t)
	defer rdb.FlushDB(ctx)
	// The large tariff sells to the bench client; the rest of the bench
	// snapshot is laid over a snapshot read from the database, fresh maps
	// only, as the loader's own are shared.
	e.load = func(ctx context.Context) (*routing.Snapshot, error) {
		s, err := store.LoadSnapshot(ctx, pool)
		if err != nil {
			return nil, err
		}
		b := benchSnapshot()
		s.Trunks, s.Connections = b.Trunks, b.Connections
		cl := b.Clients[1]
		cl.TariffID = 1
		s.Clients = map[int64]routing.Client{1: cl}
		plan := b.Plans[1]
		plan.AllowLoss = true
		s.Plans = map[int64]routing.Plan{1: plan}
		for id := int64(2); id <= 4; id++ {
			s.Tariffs[100+id] = b.Tariffs[id]
		}
		for id, c := range s.Connections {
			c.VendorTariffID += 100
			s.Connections[id] = c
		}
		return s, nil
	}
	runtime.GC()
	start := time.Now()
	if err := e.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("%s: first load %v, heap in use after it %d MiB", name, time.Since(start).Round(time.Millisecond), m.HeapInuse>>20)

	const run = 35 * time.Second
	runCtx, cancel := context.WithTimeout(ctx, run)
	defer cancel()
	var reloading atomic.Bool
	var reloads []time.Duration
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
			}
			// A thousand rates of the large tariff change before each.
			if _, err := pool.Exec(ctx, `update rates set price_per_min = price_per_min + 1 where tariff_id = 1 and id <= 1000`); err != nil {
				t.Error(err)
			}
			reloading.Store(true)
			start := time.Now()
			if err := e.Reload(ctx); err != nil {
				t.Error(err)
			}
			reloads = append(reloads, time.Since(start))
			reloading.Store(false)
		}
	}()
	var peak atomic.Uint64
	go func() {
		var m runtime.MemStats
		for runCtx.Err() == nil {
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	var mu sync.Mutex
	var during, calm []time.Duration
	refused := 0
	var wg sync.WaitGroup
	tick := time.NewTicker(time.Second / time.Duration(cps))
	for n := 0; runCtx.Err() == nil; n++ {
		<-tick.C
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			in := call("rl" + strconv.Itoa(n))
			in.Dst = "90" + strconv.Itoa(1000000+n*7%500000) + "77"
			busy := reloading.Load()
			start := time.Now()
			a := e.Authorize(ctx, in)
			took := time.Since(start)
			mu.Lock()
			defer mu.Unlock()
			if !a.Allowed {
				refused++
				return
			}
			if busy || reloading.Load() {
				during = append(during, took)
			} else {
				calm = append(calm, took)
			}
		}(n)
	}
	tick.Stop()
	wg.Wait()
	<-done
	p := func(d []time.Duration, q float64) time.Duration {
		if len(d) == 0 {
			return 0
		}
		slices.Sort(d)
		return d[min(len(d)-1, int(float64(len(d))*q))].Round(10 * time.Microsecond)
	}
	all := append(slices.Clone(during), calm...)
	t.Logf("%s: %d decisions (%d refused), reloads %v; p50 %v, p99 %v, max %v; while reloading (%d): p99 %v, max %v; otherwise p99 %v; heap in use at most %d MiB",
		name, len(all), refused, reloads, p(all, 0.5), p(all, 0.99), p(all, 1), len(during), p(during, 0.99), p(during, 1), p(calm, 0.99), peak.Load()>>20)
	if refused > 0 {
		t.Errorf("%s: %d calls refused", name, refused)
	}
}
