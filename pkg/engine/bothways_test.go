// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
)

// The limits of a trunk and of a client count the calls they place and the
// calls delivered to them together (ADR-0042).

// fromPBX is a call placed by trunk 7 ("pbx"), signed in.
func fromPBX(id string) authorizeRequest {
	in := call(id)
	in.AuthUser, in.SrcIP = "pbx", "203.0.113.9"
	return in
}

// bothWays is deliverEngine where the calls of trunk 7 go to the vendors
// and every other call is delivered to trunk 7. client, when not nil,
// moves trunk 7 to a client of its own (3), with the limits it sets.
func bothWays(t *testing.T, set func(*routing.Trunk), client func(*routing.Client)) (*Engine, *redis.Client, *time.Time) {
	e, rdb, clock, _ := deliverEngineSnap(t, func(snap *routing.Snapshot) {
		for i := range snap.Trunks {
			if snap.Trunks[i].ID == 7 {
				set(&snap.Trunks[i])
				if client != nil {
					snap.Trunks[i].ClientID = 3
				}
			}
		}
		if client != nil {
			cl := routing.Client{ID: 3, Active: true, Balance: 100 * rating.Micro, TariffID: 1, RoutingPlanID: 1}
			client(&cl)
			snap.Clients[3] = cl
		}
	})
	e.Adjust = func(ci CallInfo, d routing.Decision) routing.Decision {
		if d.TrunkID != 7 {
			d.Routes = []routing.Route{routing.ToTrunk(7, ci.Dst)}
		}
		return d
	}
	return e, rdb, clock
}

func card(t *testing.T, rdb *redis.Client, key string) int64 {
	t.Helper()
	n, err := rdb.ZCard(context.Background(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func end(t *testing.T, e *Engine, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := e.Event(context.Background(), eventRequest{V: 1, CallID: id, Event: "ended"}); err != nil {
			t.Fatal(err)
		}
	}
}

// A trunk that may have one call at a time has it in either direction: a
// call it places keeps calls from being delivered to it, and a call
// delivered to it keeps it from placing one.
func TestTrunkLimitBothWays(t *testing.T) {
	e, rdb, _ := bothWays(t, func(tr *routing.Trunk) { tr.MaxCalls = 1 }, nil)
	ctx := context.Background()
	if a := e.Authorize(ctx, fromPBX("out1")); !a.Allowed || a.Route.URI != "sip:905321112233@a:5060;transport=udp" {
		t.Fatalf("the trunk's own call: %+v %+v", a, a.Route)
	}
	if n := card(t, rdb, "cc:t:7"); n != 1 {
		t.Fatalf("the trunk's calls: %d", n)
	}
	if b := e.Authorize(ctx, call("in1")); b.Allowed || b.SIPCode != 503 || b.Reason != "all routes busy" {
		t.Fatalf("a call to the trunk while it has one of its own: %+v", b)
	}
	end(t, e, "out1")
	if b := e.Authorize(ctx, call("in2")); !b.Allowed || b.Route.URI != "sip:pbx@10.0.0.5:5060" {
		t.Fatalf("a call to the trunk once it is free: %+v %+v", b, b.Route)
	}
	if c := e.Authorize(ctx, fromPBX("out2")); c.Allowed || c.SIPCode != 503 || c.Reason != "trunk call limit reached" {
		t.Fatalf("the trunk's own call while one is delivered to it: %+v", c)
	}
	end(t, e, "in2")
	if n := card(t, rdb, "cc:t:7"); n != 0 {
		t.Fatalf("the trunk's calls at the end: %d", n)
	}
	nothingLeft(t, rdb, nil, "out1", "in1", "in2", "out2")
	if n, _ := rdb.Exists(ctx, "cc:s:7").Result(); n != 0 {
		t.Fatal("the set used before ADR-0042 was written")
	}
}

// Calls a second likewise: one counter for both directions.
func TestTrunkCPSBothWays(t *testing.T) {
	e, rdb, clock := bothWays(t, func(tr *routing.Trunk) { tr.MaxCPS = 1 }, nil)
	ctx := context.Background()
	if a := e.Authorize(ctx, fromPBX("out1")); !a.Allowed {
		t.Fatalf("the trunk's own call: %+v", a)
	}
	if b := e.Authorize(ctx, call("in1")); b.Allowed || b.SIPCode != 503 {
		t.Fatalf("a call to the trunk within the same second: %+v", b)
	}
	*clock = clock.Add(time.Second)
	if b := e.Authorize(ctx, call("in2")); !b.Allowed {
		t.Fatalf("a call to the trunk in the next second: %+v", b)
	}
	if c := e.Authorize(ctx, fromPBX("out2")); c.Allowed || c.Reason != "trunk cps limit reached" {
		t.Fatalf("the trunk's own call within that second: %+v", c)
	}
	end(t, e, "out1", "in2")
	nothingLeft(t, rdb, nil, "out1", "in1", "in2", "out2")
}

// A call delivered to a client's trunk counts against that client's own
// limit, the one the calls it places count against as well.
func TestDeliveredCallClientLimit(t *testing.T) {
	e, rdb, clock := bothWays(t, func(*routing.Trunk) {}, func(cl *routing.Client) { cl.MaxCalls = 1 })
	ctx := context.Background()
	// The client's own call fills its one place: the trunk, with no limit
	// of its own, takes no call.
	if a := e.Authorize(ctx, fromPBX("out1")); !a.Allowed {
		t.Fatalf("the client's own call: %+v", a)
	}
	if n := card(t, rdb, clientCalls(3)); n != 1 {
		t.Fatalf("the client's calls: %d", n)
	}
	if b := e.Authorize(ctx, call("in1")); b.Allowed || b.SIPCode != 503 || b.Reason != "all routes busy" {
		t.Fatalf("a call to the client while it has one of its own: %+v", b)
	}
	end(t, e, "out1")
	// A call delivered to it counts among its calls and its caller's.
	if b := e.Authorize(ctx, call("in2")); !b.Allowed || b.Route.URI != "sip:pbx@10.0.0.5:5060" {
		t.Fatalf("a call to the client once it is free: %+v %+v", b, b.Route)
	}
	if n, m := card(t, rdb, clientCalls(3)), card(t, rdb, clientCalls(1)); n != 1 || m != 1 {
		t.Fatalf("calls of the client delivered to and of the caller: %d %d", n, m)
	}
	if c := e.Authorize(ctx, fromPBX("out2")); c.Allowed || c.SIPCode != 503 || c.Reason != "call limit reached" {
		t.Fatalf("the client's own call while one is delivered to it: %+v", c)
	}
	// Moving on to the trunk's other registration is the same call to the
	// client: it keeps its one place.
	*clock = clock.Add(time.Second)
	if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "in2", Event: "failed", SIPCode: 408}); err != nil || ev.Route == nil || ev.Route.URI != "sip:pbx@203.0.113.9:5060" {
		t.Fatalf("next registration: %+v %v", ev.Route, err)
	}
	if n := card(t, rdb, clientCalls(3)); n != 1 {
		t.Fatalf("the client's calls after moving on: %d", n)
	}
	// With nowhere left to go the place is given back.
	if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "in2", Event: "failed", SIPCode: 408}); err != nil || ev.Route != nil {
		t.Fatalf("after the last registration: %+v %v", ev.Route, err)
	}
	if n, m := card(t, rdb, clientCalls(3)), card(t, rdb, clientCalls(1)); n != 0 || m != 0 {
		t.Fatalf("calls after the last failure: %d %d", n, m)
	}
	if b := e.Authorize(ctx, call("in3")); !b.Allowed {
		t.Fatalf("a call to the client once it is free again: %+v", b)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "in3", Event: "answered"})
	end(t, e, "in3")
	if n := card(t, rdb, clientCalls(3)); n != 0 {
		t.Fatalf("the client's calls after the end: %d", n)
	}
	nothingLeft(t, rdb, map[string]bool{"in3": true}, "out1", "in1", "in2", "out2", "in3")
}

// The client's calls a second: one counter for both directions, and a call
// moving on to another registration within its second is not a new one.
func TestDeliveredCallClientCPS(t *testing.T) {
	e, _, clock := bothWays(t, func(*routing.Trunk) {}, func(cl *routing.Client) { cl.MaxCPS = 1 })
	ctx := context.Background()
	if a := e.Authorize(ctx, call("in1")); !a.Allowed {
		t.Fatalf("a call to the client: %+v", a)
	}
	if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "in1", Event: "failed", SIPCode: 408}); err != nil || ev.Route == nil {
		t.Fatalf("moving on within the second: %+v %v", ev.Route, err)
	}
	if b := e.Authorize(ctx, fromPBX("out1")); b.Allowed || b.Reason != "cps limit reached" {
		t.Fatalf("the client's own call within the second: %+v", b)
	}
	*clock = clock.Add(time.Second)
	if b := e.Authorize(ctx, fromPBX("out2")); !b.Allowed {
		t.Fatalf("the client's own call in the next second: %+v", b)
	}
	if c := e.Authorize(ctx, call("in2")); c.Allowed {
		t.Fatalf("a call to the client within that second: %+v", c)
	}
	end(t, e, "in1", "out2")
}

// A delivered call whose end never came gives its place among its client's
// calls back when its state expires.
func TestDeliveredCallClientExpires(t *testing.T) {
	e, rdb, clock := bothWays(t, func(*routing.Trunk) {}, func(cl *routing.Client) { cl.MaxCalls = 1 })
	ctx := context.Background()
	start := *clock
	if a := e.Authorize(ctx, call("x1")); !a.Allowed {
		t.Fatalf("call: %+v", a)
	}
	c, err := e.calls.Get(ctx, "x1")
	if err != nil || c == nil {
		t.Fatalf("state: %v %v", c, err)
	}
	*clock = start.Add(time.Duration(c.TTL) * time.Second)
	if n, err := e.SweepExpired(ctx); err != nil || n != 1 {
		t.Fatalf("expired: %d %v", n, err)
	}
	if n := card(t, rdb, clientCalls(3)); n != 0 {
		t.Fatalf("the client's calls after the expiry: %d", n)
	}
	nothingLeft(t, rdb, nil, "x1")
}

// A call from a client to its own trunk, or from a trunk back to itself,
// counts once: the caller's side was checked at the admission, and moving
// on to another registration keeps the call's place there.
func TestDeliveredCallOwnCounts(t *testing.T) {
	t.Run("own client", func(t *testing.T) {
		e, rdb, clock, alone := deliverEngineSnap(t, func(snap *routing.Snapshot) {
			cl := snap.Clients[1]
			cl.MaxCalls, cl.MaxCPS = 1, 1
			snap.Clients[1] = cl
		})
		*alone = false // the vendors after the trunk
		ctx := context.Background()
		if a := e.Authorize(ctx, call("o1")); !a.Allowed {
			t.Fatalf("a call to the client's own trunk: %+v", a)
		}
		ownMoves(t, e, rdb, clock, "o1", clientCalls(1))
	})
	t.Run("own trunk", func(t *testing.T) {
		e, rdb, clock, alone := deliverEngine(t, func(tr *routing.Trunk) { tr.MaxCalls, tr.MaxCPS = 1, 1 })
		*alone = false
		ctx := context.Background()
		if a := e.Authorize(ctx, fromPBX("o1")); !a.Allowed || a.Route.URI != "sip:pbx@10.0.0.5:5060" {
			t.Fatalf("a call from the trunk back to itself: %+v %+v", a, a.Route)
		}
		ownMoves(t, e, rdb, clock, "o1", "cc:t:7")
	})
}

// ownMoves moves call id on from the first registration of trunk 7 to the
// second, then to a vendor: all along the call keeps its one place in set,
// where it counts as the caller's; its end gives the place back.
func ownMoves(t *testing.T, e *Engine, rdb *redis.Client, clock *time.Time, id, set string) {
	t.Helper()
	ctx := context.Background()
	for _, want := range []string{"sip:pbx@203.0.113.9:5060", "sip:905321112233@a:5060;transport=udp"} {
		*clock = clock.Add(100 * time.Millisecond)
		if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: id, Event: "failed", SIPCode: 408}); err != nil || ev.Route == nil || ev.Route.URI != want {
			t.Fatalf("moving on to %s: %+v %v", want, ev.Route, err)
		}
		if n := card(t, rdb, set); n != 1 {
			t.Fatalf("calls in %s after moving on to %s: %d", set, want, n)
		}
	}
	end(t, e, id)
	if n := card(t, rdb, set); n != 0 {
		t.Fatalf("calls in %s at the end: %d", set, n)
	}
	nothingLeft(t, rdb, nil, id)
}

// The calls trunks placed under an engine from before ADR-0042 join the
// trunk's one set when the engine starts; repeating it changes nothing.
func TestMigrateTrunkCounts(t *testing.T) {
	_, _, rdb := testEngine(t)
	ctx := context.Background()
	rdb.ZAdd(ctx, "cc:s:5", redis.Z{Score: 100, Member: "old"}, redis.Z{Score: 300, Member: "both"})
	rdb.ZAdd(ctx, "cc:t:5", redis.Z{Score: 200, Member: "new"}, redis.Z{Score: 200, Member: "both"})
	for i, want := range []int{1, 0} {
		n, err := MigrateTrunkCounts(ctx, rdb)
		if err != nil || n != want {
			t.Fatalf("run %d: %d %v", i+1, n, err)
		}
	}
	got, err := rdb.ZRangeWithScores(ctx, "cc:t:5", 0, -1).Result()
	if err != nil || len(got) != 3 || got[0].Member != "old" || got[1].Member != "new" || got[2].Member != "both" || got[2].Score != 300 {
		t.Fatalf("the trunk's set: %+v %v", got, err)
	}
	if n, _ := rdb.Exists(ctx, "cc:s:5").Result(); n != 0 {
		t.Fatal("the old set is still there")
	}
}
