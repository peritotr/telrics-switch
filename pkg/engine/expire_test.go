// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// A clock of these tests' own, far from the others' (ADR-0029).
var expireClock = time.Date(2028, 3, 14, 9, 0, 0, 0, time.UTC)

// expireEngine admits call id at expireClock and returns how long the call's
// state lasts.
func expireEngine(t *testing.T, id string) (*Engine, *memSink, *time.Time, time.Duration) {
	e, sink, _ := testEngine(t)
	clock := expireClock
	e.now = func() time.Time { return clock }
	if a := e.Authorize(context.Background(), call(id)); !a.Allowed {
		t.Fatalf("refused: %+v", a)
	}
	c, err := e.calls.Get(context.Background(), id)
	if err != nil || c == nil {
		t.Fatalf("state: %v %v", c, err)
	}
	return e, sink, &clock, time.Duration(c.TTL) * time.Second
}

func sweepExpired(t *testing.T, e *Engine) int {
	t.Helper()
	n, err := e.SweepExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// An answered call whose end never came (the SIP core restarted, or the
// engine was away when it ended) is recorded when its state expires: as
// answered, ending at its answer, so nothing is charged that is not known.
func TestExpiredAnsweredCall(t *testing.T) {
	e, sink, clock, ttl := expireEngine(t, "lost")
	ctx := context.Background()
	*clock = clock.Add(3 * time.Second)
	e.Event(ctx, eventRequest{V: 1, CallID: "lost", Event: "ringing"})
	*clock = clock.Add(2 * time.Second)
	answer := *clock
	e.Event(ctx, eventRequest{V: 1, CallID: "lost", Event: "answered"})
	expired := mExpired.Value()

	*clock = expireClock.Add(ttl - time.Second)
	if n := sweepExpired(t, e); n != 0 {
		t.Fatalf("%d calls expired before their time", n)
	}
	*clock = expireClock.Add(ttl)
	if n := sweepExpired(t, e); n != 1 {
		t.Fatalf("%d calls expired, want 1", n)
	}
	if len(sink.recs) != 1 {
		t.Fatalf("records: %+v", sink.recs)
	}
	r := sink.recs[0]
	if r.CallID != "lost" || r.Leg != 1 || r.SIPCode != 200 || r.AnswerAt == nil || !r.AnswerAt.Equal(answer) ||
		!r.EndAt.Equal(answer) || !r.StartAt.Equal(expireClock) || r.PDDMs != 3000 || r.ConnectionID == nil || *r.ConnectionID != 1 {
		t.Fatalf("record: %+v", r)
	}
	if mExpired.Value() != expired+1 {
		t.Fatal("not counted")
	}
	// The call is gone everywhere: its place in the counts, the money held.
	if c, _ := e.calls.Get(ctx, "lost"); c != nil {
		t.Fatalf("state left: %+v", c)
	}
	rdb := e.calls.(*RedisCalls).rdb
	if n := rdb.ZCard(ctx, expiryKey).Val() + rdb.ZCard(ctx, dueKey).Val(); n != 0 {
		t.Fatalf("%d entries left in the expiry and due sets", n)
	}
	if held, _ := rdb.Get(ctx, holdKeys(1)[2]).Int64(); held != 0 {
		t.Fatalf("still held: %d", held)
	}
	// An end reported after all is ignored, as for any finished call.
	e.Event(ctx, eventRequest{V: 1, CallID: "lost", Event: "ended"})
	*clock = clock.Add(time.Hour)
	if n := sweepExpired(t, e); n != 0 || len(sink.recs) != 1 {
		t.Fatalf("again: %d expired, %d records", n, len(sink.recs))
	}
}

// A call never answered and never reported failed fails with 408 when its
// state expires.
func TestExpiredUnansweredCall(t *testing.T) {
	e, sink, clock, ttl := expireEngine(t, "silent")
	*clock = expireClock.Add(ttl + 10*time.Second)
	if n := sweepExpired(t, e); n != 1 || len(sink.recs) != 1 {
		t.Fatalf("%d expired, records %+v", n, sink.recs)
	}
	r := sink.recs[0]
	if r.SIPCode != 408 || r.AnswerAt != nil || !r.EndAt.Equal(expireClock.Add(ttl)) || r.PDDMs != 0 {
		t.Fatalf("record: %+v", r)
	}
	// The client may call again: its place among its calls is free.
	*clock = clock.Add(time.Second)
	if a := e.Authorize(context.Background(), call("next")); !a.Allowed {
		t.Fatalf("next call refused: %+v", a)
	}
}

// A call that ended as it should leaves nothing to expire.
func TestEndedCallDoesNotExpire(t *testing.T) {
	e, sink, clock, ttl := expireEngine(t, "fine")
	ctx := context.Background()
	e.Event(ctx, eventRequest{V: 1, CallID: "fine", Event: "answered"})
	*clock = clock.Add(30 * time.Second)
	e.Event(ctx, eventRequest{V: 1, CallID: "fine", Event: "ended"})
	*clock = expireClock.Add(ttl + time.Minute)
	if n := sweepExpired(t, e); n != 0 || len(sink.recs) != 1 {
		t.Fatalf("%d expired, %d records", n, len(sink.recs))
	}
}

// The engine dies after letting the hold go and before the state: the
// next pass (after the lease) writes the record all the same. The record
// may then be written twice; the stream and the rater keep it once.
func TestExpiredCallAfterCrash(t *testing.T) {
	e, sink, clock, ttl := expireEngine(t, "crash")
	ctx := context.Background()
	*clock = expireClock.Add(ttl)
	ids, err := e.calls.Expired(ctx, *clock, 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("claimed %v %v", ids, err)
	}
	c, _ := e.calls.Get(ctx, "crash")
	if err := e.calls.Release(ctx, "crash", c, 0, 0); err != nil {
		t.Fatal(err)
	}
	// While the claim lasts no other pass takes the call.
	if n := sweepExpired(t, e); n != 0 {
		t.Fatalf("claimed twice: %d", n)
	}
	*clock = clock.Add(expiryLease)
	if n := sweepExpired(t, e); n != 1 || len(sink.recs) != 1 || sink.recs[0].SIPCode != 408 {
		t.Fatalf("%d expired, records %+v", n, sink.recs)
	}
	// State gone but its entry left (a finish that half happened): dropped.
	e.calls.(*RedisCalls).rdb.ZAdd(ctx, expiryKey, redis.Z{Score: float64(clock.Unix()), Member: "ghost"})
	*clock = clock.Add(time.Second)
	if n := sweepExpired(t, e); n != 1 || len(sink.recs) != 1 {
		t.Fatalf("ghost: %d, records %d", n, len(sink.recs))
	}
	if n := e.calls.(*RedisCalls).rdb.ZCard(ctx, expiryKey).Val(); n != 0 {
		t.Fatalf("%d entries left", n)
	}
}
