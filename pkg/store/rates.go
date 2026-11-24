// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"maps"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
)

// Rates are by far the largest part of a snapshot: a tariff priced by number
// range holds millions of them, and reading 5 million takes about 9 seconds
// with 2.3 GiB passing through memory (T-30). The engine reloads every 10
// seconds for the balances, so rates are read again only where they changed
// (ADR-0048): every statement that writes rates notes the tariffs and
// prefixes it touched in rate_changes with the writing transaction's id (a
// trigger, so writes from outside the API count too). A load remembers the
// database snapshot it read under; the next one reads the notes of
// transactions that snapshot did not see, whatever order they committed in,
// and reads those prefixes again, or the whole tariff when the note has no
// prefix or a rate of the tariff starts or ends in force. The rest is taken
// from the previous load: shared, never changed, by the snapshots built from
// it; a changed tariff is a copy. Every FullReloadEvery everything is read
// again, should a change ever slip past the notes.

// tariffRates is what one tariff's rates in force make of a snapshot.
type tariffRates struct {
	// until is when a rate of the tariff next starts or ends; zero: never.
	until time.Time
	// single is the prefixes with one rate for all hours; nil when the
	// tariff has no rate in force at all.
	single map[string]rating.Rate
	// timed is the prefixes with rates by day or hour; nil when none.
	timed map[string][]rating.Rate
}

// notesKept is how long rate_changes keeps a note (the migration's interval).
// A load whose previous one is older than half of it reads everything.
const notesKept = time.Hour

type rateCache struct {
	mu     sync.Mutex
	byID   map[int64]*tariffRates
	seen   string    // the database snapshot byID was read under
	at     time.Time // when that load started
	fullAt time.Time // when everything was last read
	idle   *time.Timer
}

var rates = &rateCache{}

// fullEvery is FullReloadEvery in nanoseconds.
var fullEvery atomic.Int64

func init() {
	every := 5 * time.Minute
	if v := os.Getenv("RELOAD_FULL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			every = time.Duration(n) * time.Second
		}
	}
	fullEvery.Store(int64(every))
}

// SetFullReload sets how often a snapshot load reads every rate again, even
// those that did not change; zero reads them all on every load (the
// behaviour before T-30). The default is RELOAD_FULL_SEC, or 5 minutes; at
// most half an hour is used, see notesKept.
func SetFullReload(every time.Duration) { fullEvery.Store(int64(every)) }

// FullReloadEvery is the interval set by SetFullReload.
func FullReloadEvery() time.Duration { return time.Duration(fullEvery.Load()) }

// RateLoad says what a snapshot load did with the rates, for the measures.
type RateLoad struct {
	Full     bool // every rate was read
	Tariffs  int  // tariffs read whole
	Prefixes int  // prefixes read again in tariffs otherwise kept
	Duration time.Duration
}

var lastRateLoad atomic.Pointer[RateLoad]

// LastRateLoad is what the last snapshot load in this process did with the
// rates; nil before the first.
func LastRateLoad() *RateLoad { return lastRateLoad.Load() }

// previous returns the last load's rates and the database snapshot they were
// read under, or nil when this load must read everything.
func (c *rateCache) previous(started time.Time) (map[int64]*tariffRates, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	every := min(FullReloadEvery(), notesKept/2)
	if every <= 0 || c.byID == nil || started.Sub(c.fullAt) >= every || started.Sub(c.at) >= notesKept/2 {
		return nil, ""
	}
	return c.byID, c.seen
}

// keep remembers the rates of a load for the next one.
func (c *rateCache) keep(byID map[int64]*tariffRates, seen string, full bool, started time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	every := min(FullReloadEvery(), notesKept/2)
	if every <= 0 {
		c.byID = nil
		return
	}
	c.byID, c.seen, c.at = byID, seen, started
	if full {
		c.fullAt = started
	}
	// A process that stops loading (the API's simulator, used now and then)
	// does not keep millions of rates for nothing.
	if c.idle == nil {
		c.idle = time.AfterFunc(2*every, c.drop)
	} else {
		c.idle.Reset(2 * every)
	}
}

func (c *rateCache) drop() {
	c.mu.Lock()
	c.byID = nil
	c.mu.Unlock()
}

// loadRates fills s.Tariffs and s.Timed for the tariffs in zones, reading
// only what changed since the previous load unless cached is false.
func loadRates(ctx context.Context, q Querier, s *routing.Snapshot, zones map[int64]*time.Location, owners map[int64]int64, cals map[int64]rating.Calendar, cached bool) error {
	started := time.Now()
	// The snapshot is taken before the notes and the rates are read: a write
	// committed in between is read now and once more next time, never not
	// at all.
	var seen string
	var now time.Time
	rows, err := q.Query(ctx, `select pg_current_snapshot()::text, now()`)
	if err != nil {
		return err
	}
	if _, err := pgx.ForEachRow(rows, []any{&seen, &now}, func() error { return nil }); err != nil {
		return err
	}
	var prev map[int64]*tariffRates
	var before string
	if cached {
		prev, before = rates.previous(started)
	}
	full := prev == nil

	// What to read: every tariff, or those not kept, whose rates came into
	// or went out of force, or noted whole, and the noted prefixes of the
	// others.
	var whole []int64
	partly := map[int64][]string{}
	if full {
		for id := range zones {
			whole = append(whole, id)
		}
	} else {
		rows, err := q.Query(ctx, `select distinct tariff_id, prefix from rate_changes where not pg_visible_in_snapshot(xid, $1::pg_snapshot)`, before)
		if err != nil {
			return err
		}
		all := map[int64]bool{}
		var tid int64
		var prefix *string
		if _, err := pgx.ForEachRow(rows, []any{&tid, &prefix}, func() error {
			if prefix == nil {
				all[tid] = true
			} else {
				partly[tid] = append(partly[tid], *prefix)
			}
			return nil
		}); err != nil {
			return err
		}
		for id := range zones {
			t := prev[id]
			if t == nil || all[id] || !t.until.IsZero() && !now.Before(t.until) {
				whole = append(whole, id)
				delete(partly, id)
			}
		}
	}
	readWhole := make(map[int64]bool, len(whole))
	for _, id := range whole {
		readWhole[id] = true
	}
	read, err := readRates(ctx, q, now, full, whole, nil)
	if err != nil {
		return err
	}
	byID := make(map[int64]*tariffRates, len(zones))
	for id := range zones {
		if t := read[id]; t != nil {
			byID[id] = t
		} else if !full && !readWhole[id] {
			byID[id] = prev[id]
		} else {
			byID[id] = &tariffRates{}
		}
	}
	prefixes := 0
	for id, list := range partly {
		if byID[id] == nil {
			continue // a tariff that is gone
		}
		prefixes += len(list)
		if byID[id], err = rereadPrefixes(ctx, q, now, id, list, byID[id]); err != nil {
			return err
		}
	}
	for id, t := range byID {
		if t.single == nil {
			continue
		}
		s.Tariffs[id] = t.single
		if t.timed != nil {
			s.Timed[id] = routing.TimedTariff{Loc: zones[id], Cal: cals[owners[id]], Rates: t.timed}
		}
	}
	if !cached {
		return nil
	}
	rates.keep(byID, seen, full, started)
	lastRateLoad.Store(&RateLoad{Full: full, Tariffs: len(whole), Prefixes: prefixes, Duration: time.Since(started)})
	return nil
}

// rereadPrefixes returns old with the given prefixes read again: a copy, as
// old stays in use by the snapshot that has it.
func rereadPrefixes(ctx context.Context, q Querier, now time.Time, tariff int64, prefixes []string, old *tariffRates) (*tariffRates, error) {
	read, err := readRates(ctx, q, now, false, []int64{tariff}, prefixes)
	if err != nil {
		return nil, err
	}
	t := &tariffRates{until: old.until, single: maps.Clone(old.single), timed: maps.Clone(old.timed)}
	if t.single == nil {
		t.single = map[string]rating.Rate{}
	}
	if t.timed == nil {
		t.timed = map[string][]rating.Rate{}
	}
	for _, p := range prefixes {
		delete(t.single, p)
		delete(t.timed, p)
	}
	if n := read[tariff]; n != nil {
		maps.Copy(t.single, n.single)
		maps.Copy(t.timed, n.timed)
		// The old time stays: a rate that set it may be gone, and reading
		// the tariff a little early does no harm.
		if !n.until.IsZero() && (t.until.IsZero() || n.until.Before(t.until)) {
			t.until = n.until
		}
	}
	if len(t.timed) == 0 {
		t.timed = nil
	}
	if len(t.single) == 0 && t.timed == nil {
		t.single = nil
	}
	return t, nil
}

// readRates reads the rates in force at now of the tariffs given, or of all
// when all is set, with when each tariff's rates next start or end. With
// prefixes, only those prefixes of the one tariff given.
func readRates(ctx context.Context, q Querier, now time.Time, all bool, ids []int64, prefixes []string) (map[int64]*tariffRates, error) {
	out := map[int64]*tariffRates{}
	if !all && len(ids) == 0 {
		return out, nil
	}
	where, args := "", []any{now}
	switch {
	case all:
	case prefixes != nil:
		where, args = " and tariff_id = $2 and prefix = any($3)", append(args, ids[0], prefixes)
	default:
		where, args = " and tariff_id = any($2)", append(args, ids)
	}
	rows, err := q.Query(ctx, `
		select id, tariff_id, prefix, price_per_min, connect_fee, initial_sec, increment_sec, grace_sec, blocked,
		       days, time_from, time_to, effective_from, effective_to
		from rates where effective_from <= $1 and (effective_to is null or effective_to > $1)`+where, args...)
	if err != nil {
		return nil, err
	}
	var tid int64
	var rate rating.Rate
	var to *time.Time
	until := map[int64]time.Time{}
	sooner := func(tid int64, at time.Time) {
		if until[tid].IsZero() || at.Before(until[tid]) {
			until[tid] = at
		}
	}
	inForce := map[int64]map[string][]rating.Rate{}
	if _, err = pgx.ForEachRow(rows, []any{&rate.ID, &tid, &rate.Prefix, &rate.PricePerMin, &rate.ConnectFee, &rate.InitialSec, &rate.IncrementSec, &rate.GraceSec, &rate.Blocked,
		&rate.Days, &rate.From, &rate.To, &rate.EffectiveFrom, &to}, func() error {
		if inForce[tid] == nil {
			inForce[tid] = map[string][]rating.Rate{}
		}
		inForce[tid][rate.Prefix] = append(inForce[tid][rate.Prefix], rate)
		if to != nil {
			sooner(tid, *to)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// The next rate to come into force, tariff by tariff (an index range
	// each).
	var next string
	switch {
	case all:
		next = `select t.id, (select min(r.effective_from) from rates r where r.tariff_id = t.id and r.effective_from > $1) from tariffs t`
	case prefixes != nil:
		next = `select $2::bigint, min(effective_from) from rates where tariff_id = $2 and prefix = any($3) and effective_from > $1`
	default:
		next = `select t.id, (select min(r.effective_from) from rates r where r.tariff_id = t.id and r.effective_from > $1) from tariffs t where t.id = any($2)`
	}
	rows, err = q.Query(ctx, next, args...)
	if err != nil {
		return nil, err
	}
	var from *time.Time
	if _, err = pgx.ForEachRow(rows, []any{&tid, &from}, func() error {
		if from != nil {
			sooner(tid, *from)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	for tid, byPrefix := range inForce {
		t := &tariffRates{until: until[tid], single: make(map[string]rating.Rate, len(byPrefix))}
		for prefix, list := range byPrefix {
			list = rating.Tidy(list)
			if len(list) == 1 && !list[0].Limited() {
				t.single[prefix] = list[0]
				continue
			}
			if t.timed == nil {
				t.timed = map[string][]rating.Rate{}
			}
			t.timed[prefix] = list
		}
		out[tid] = t
	}
	// A tariff without a rate in force may still get one later.
	for tid, at := range until {
		if out[tid] == nil {
			out[tid] = &tariffRates{until: at}
		}
	}
	return out, nil
}
