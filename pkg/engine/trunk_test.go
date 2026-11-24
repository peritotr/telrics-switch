// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"softswitch/core/pkg/routing"
)

// A clock of these tests' own, far from the others' "now".
var trunkClock = time.Date(2028, 5, 9, 10, 0, 0, 0, time.UTC)

// deliverEngine is the registration engine with trunk 7 ("pbx") registered
// twice (the binding behind NAT last, so it is tried first) and given the
// limits and codecs of set. The caller's trunk (1) lets PCMU and PCMA
// through; the client and the vendors have no limits. route says where
// calls go: to the trunk alone, or to the trunk and then to the vendors.
func deliverEngine(t *testing.T, set func(*routing.Trunk)) (e *Engine, rdb *redis.Client, clock *time.Time, alone *bool) {
	return deliverEngineSnap(t, func(snap *routing.Snapshot) {
		for i := range snap.Trunks {
			if snap.Trunks[i].ID == 7 {
				set(&snap.Trunks[i])
			}
		}
	})
}

// deliverEngineSnap is deliverEngine with the whole snapshot to change.
func deliverEngineSnap(t *testing.T, set func(*routing.Snapshot)) (e *Engine, rdb *redis.Client, clock *time.Time, alone *bool) {
	e, _ = regEngine(t)
	rdb = e.Registry.(*RedisRegistry).rdb.(*redis.Client)
	snap := regSnapshot()
	snap.Trunks[0].Codecs = []string{"PCMU", "PCMA"}
	cl := snap.Clients[1]
	cl.MaxCalls = 0
	snap.Clients[1] = cl
	c1 := snap.Connections[1]
	c1.MaxCalls = 0
	snap.Connections[1] = c1
	set(snap)
	e.load = func(context.Context) (*routing.Snapshot, error) { return snap, nil }
	ctx := context.Background()
	if err := e.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	now := trunkClock
	clock = &now
	e.now = func() time.Time { return *clock }
	e.Register(ctx, reg("pbx", "<sip:pbx@203.0.113.9:5060>", 3600))
	*clock = clock.Add(time.Second)
	behind := reg("pbx", "<sip:pbx@10.0.0.5:5060>", 3600)
	behind.NAT = true
	e.Register(ctx, behind)
	*clock = clock.Add(time.Second)
	only := true
	alone = &only
	e.Adjust = func(ci CallInfo, d routing.Decision) routing.Decision {
		routes := []routing.Route{routing.ToTrunk(7, ci.Dst)}
		if !*alone {
			routes = append(routes, d.Routes...)
		}
		d.Routes = routes
		return d
	}
	return e, rdb, clock, alone
}

func delivered(t *testing.T, rdb *redis.Client) int64 {
	t.Helper()
	n, err := rdb.ZCard(context.Background(), "cc:t:7").Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// nothingLeft fails the test when the state store still holds anything of
// the calls: their state, their place in a count. Money held for them is
// gone too unless they were answered: the charge of an answered call stays
// held until the rater settles it.
func nothingLeft(t *testing.T, rdb *redis.Client, answered map[string]bool, ids ...string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		if n, _ := rdb.Exists(ctx, callKey(id)).Result(); n != 0 {
			t.Errorf("%s: state left behind", id)
		}
		for _, set := range []string{"cc:t:7", allCalls, clientCalls(1), expiryKey, dueKey} {
			if _, err := rdb.ZScore(ctx, set, id).Result(); err != redis.Nil {
				t.Errorf("%s: still in %s", id, set)
			}
		}
		if ok, _ := rdb.HExists(ctx, "held:1", id).Result(); ok && !answered[id] {
			t.Errorf("%s: money still held", id)
		}
	}
}

// A trunk that may take one call at a time: while it has one, the next
// call delivered to it passes over every registration of it to the
// vendors, or is refused when it has nowhere else to go. Moving on to the
// trunk's other registration keeps the call's one place. Each call is
// offered the trunk's codecs only.
func TestDeliveredCallTrunkLimit(t *testing.T) {
	e, rdb, clock, alone := deliverEngine(t, func(tr *routing.Trunk) { tr.MaxCalls, tr.Codecs = 1, []string{"PCMA", "G729"} })
	ctx := context.Background()

	a := e.Authorize(ctx, call("d1"))
	if !a.Allowed || a.Route.URI != "sip:pbx@10.0.0.5:5060" || a.Route.Media != "rtp" || !reflect.DeepEqual(a.Route.Codecs, []string{"PCMA"}) {
		t.Fatalf("first call: %+v %+v", a, a.Route)
	}
	if n := delivered(t, rdb); n != 1 {
		t.Fatalf("calls counted on the trunk: %d", n)
	}
	// The trunk is full: the call goes to the vendors.
	*alone = false
	b := e.Authorize(ctx, call("d2"))
	if !b.Allowed || b.Route.URI != "sip:905321112233@a:5060;transport=udp" || b.Route.Media != "" ||
		!reflect.DeepEqual(b.Route.Codecs, []string{"PCMU", "PCMA"}) {
		t.Fatalf("second call, with vendors after the trunk: %+v %+v", b, b.Route)
	}
	// With nowhere else to go it is refused.
	*alone = true
	if c := e.Authorize(ctx, call("d3")); c.Allowed || c.SIPCode != 503 || c.Reason != "all routes busy" {
		t.Fatalf("third call, to the full trunk alone: %+v", c)
	}
	if n := delivered(t, rdb); n != 1 {
		t.Fatalf("calls counted on the trunk after the refusals: %d", n)
	}
	// The first call moves on to the trunk's other registration: its place
	// goes with it.
	ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "d1", Event: "failed", SIPCode: 408})
	if err != nil || ev.Route == nil || ev.Route.URI != "sip:pbx@203.0.113.9:5060" || !reflect.DeepEqual(ev.Route.Codecs, []string{"PCMA"}) {
		t.Fatalf("next registration: %+v %v", ev.Route, err)
	}
	if n := delivered(t, rdb); n != 1 {
		t.Fatalf("calls counted on the trunk after moving on: %d", n)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "d1", Event: "answered"})
	*clock = clock.Add(3 * time.Second)
	for _, id := range []string{"d1", "d2"} {
		if _, err := e.Event(ctx, eventRequest{V: 1, CallID: id, Event: "ended"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := delivered(t, rdb); n != 0 {
		t.Fatalf("calls counted on the trunk after the end: %d", n)
	}
	nothingLeft(t, rdb, map[string]bool{"d1": true, "d2": true}, "d1", "d2", "d3")
	// Free again.
	if d := e.Authorize(ctx, call("d4")); !d.Allowed {
		t.Fatalf("a call once the trunk is free: %+v", d)
	}
	// A failure with nowhere to move on to lets the place go as well.
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "d4", Event: "failed", SIPCode: 503}); err != nil {
		t.Fatal(err)
	}
	if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "d4", Event: "failed", SIPCode: 503}); err != nil || ev.Route != nil {
		t.Fatalf("after the last registration: %+v %v", ev.Route, err)
	}
	if n := delivered(t, rdb); n != 0 {
		t.Fatalf("calls counted on the trunk after the last failure: %d", n)
	}
	nothingLeft(t, rdb, nil, "d4")
}

// A trunk that may take one call a second: a second call delivered to it
// within the second is passed over, but the first call moving on to the
// trunk's other registration is not a second call.
func TestDeliveredCallTrunkCPS(t *testing.T) {
	e, rdb, clock, _ := deliverEngine(t, func(tr *routing.Trunk) { tr.MaxCPS = 1 })
	ctx := context.Background()
	if a := e.Authorize(ctx, call("r1")); !a.Allowed {
		t.Fatalf("first call: %+v", a)
	}
	if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "r1", Event: "failed", SIPCode: 408}); err != nil || ev.Route == nil {
		t.Fatalf("the first call moving on within its second: %+v %v", ev.Route, err)
	}
	if b := e.Authorize(ctx, call("r2")); b.Allowed || b.SIPCode != 503 {
		t.Fatalf("second call within the second: %+v", b)
	}
	*clock = clock.Add(time.Second)
	if c := e.Authorize(ctx, call("r3")); !c.Allowed {
		t.Fatalf("a call in the next second: %+v", c)
	}
	for _, id := range []string{"r1", "r3"} {
		e.Event(ctx, eventRequest{V: 1, CallID: id, Event: "ended"})
	}
	if n := delivered(t, rdb); n != 0 {
		t.Fatalf("calls counted on the trunk: %d", n)
	}
	nothingLeft(t, rdb, nil, "r1", "r2", "r3")
}

// A delivered call whose end never came gives its place on the trunk back
// when its state expires (ADR-0029).
func TestDeliveredCallExpires(t *testing.T) {
	e, rdb, clock, _ := deliverEngine(t, func(tr *routing.Trunk) { tr.MaxCalls = 1 })
	ctx := context.Background()
	start := *clock
	if a := e.Authorize(ctx, call("x1")); !a.Allowed {
		t.Fatalf("call: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "x1", Event: "answered"})
	c, err := e.calls.Get(ctx, "x1")
	if err != nil || c == nil {
		t.Fatalf("state: %v %v", c, err)
	}
	if b := e.Authorize(ctx, call("x2")); b.Allowed {
		t.Fatalf("second call while the first holds the trunk: %+v", b)
	}
	*clock = start.Add(time.Duration(c.TTL) * time.Second)
	if n, err := e.SweepExpired(ctx); err != nil || n != 1 {
		t.Fatalf("expired: %d %v", n, err)
	}
	if n := delivered(t, rdb); n != 0 {
		t.Fatalf("calls counted on the trunk after the expiry: %d", n)
	}
	nothingLeft(t, rdb, nil, "x1", "x2")
	// The registrations ran out long ago as well.
	e.Register(ctx, reg("pbx", "<sip:pbx@203.0.113.9:5060>", 3600))
	if b := e.Authorize(ctx, call("x3")); !b.Allowed {
		t.Fatalf("a call after the expiry: %+v", b)
	}
}

// The trunk's codecs go first, as far as the caller's trunk allows; a
// trunk with none in common with it is no route, and its registrations are
// not even looked up.
func TestDeliveredCallCodecs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dst      []string
		want     []string
		refused  bool
		lookedUp bool
		// transcode: the trunk allows translation; from: the caller's
		// codecs the SIP core is told to translate from.
		transcode bool
		from      []string
	}{
		{name: "the trunk's own order", dst: []string{"PCMA", "PCMU", "G722"}, want: []string{"PCMA", "PCMU"}, lookedUp: true},
		{name: "no list of its own", want: []string{"PCMU", "PCMA"}, lookedUp: true},
		{name: "nothing in common", dst: []string{"G729"}, refused: true},
		// The trunk lets its calls be translated (T-09): offered its own
		// list, the caller's to translate from.
		{name: "nothing in common, translated", dst: []string{"G729"}, transcode: true, want: []string{"G729"}, from: []string{"PCMU", "PCMA"}, lookedUp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, rdb, _, _ := deliverEngine(t, func(tr *routing.Trunk) { tr.Codecs, tr.Transcode = tc.dst, tc.transcode })
			rec := &recorder{}
			rdb.AddHook(rec)
			a := e.Authorize(context.Background(), call("c1"))
			if tc.refused {
				if a.Allowed || a.SIPCode != 503 || a.Reason != "no route" {
					t.Fatalf("refused: %+v", a)
				}
			} else if !a.Allowed || !reflect.DeepEqual(a.Route.Codecs, tc.want) || !reflect.DeepEqual(a.Route.Transcode, tc.from) {
				t.Fatalf("offer: %+v %+v", a, a.Route)
			}
			if n, _ := rdb.ZCard(context.Background(), transcodeCalls).Result(); (n == 1) != tc.transcode {
				t.Fatalf("calls counted as translated: %d", n)
			}
			if looked := rec.has("zrangebyscore") || rec.has("evalsha"); looked != tc.lookedUp {
				t.Fatalf("store asked: %v, want %v (%v)", looked, tc.lookedUp, rec.names())
			}
		})
	}
}

// recorder keeps the commands a client sends.
type recorder struct {
	mu   sync.Mutex
	cmds [][]any
}

func (r *recorder) DialHook(next redis.DialHook) redis.DialHook { return next }
func (r *recorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.mu.Lock()
		r.cmds = append(r.cmds, cmd.Args())
		r.mu.Unlock()
		return next(ctx, cmd)
	}
}
func (r *recorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		r.mu.Lock()
		for _, c := range cmds {
			r.cmds = append(r.cmds, c.Args())
		}
		r.mu.Unlock()
		return next(ctx, cmds)
	}
}

func (r *recorder) take() [][]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.cmds
	r.cmds = nil
	return out
}

func (r *recorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.cmds {
		out = append(out, c[0].(string))
	}
	return out
}

func (r *recorder) has(name string) bool {
	for _, n := range r.names() {
		if n == name {
			return true
		}
	}
	return false
}

// An ordinary call to a vendor costs one trip to the store, as before
// calls could be delivered to trunks: the admission script, with the same
// keys and the same arguments (the layout is spelled out here so that a
// change to it shows); moving on to the next vendor is the call's state
// and the script that moves it.
func TestVendorCallStoreTrips(t *testing.T) {
	e, _, rdb := testEngine(t)
	ctx := context.Background()
	at := trunkClock
	e.now = func() time.Time { return at }
	if a := e.Authorize(ctx, call("warm")); !a.Allowed { // loads the scripts
		t.Fatalf("warm-up: %+v", a)
	}
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "warm", Event: "failed", SIPCode: 503}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "warm", Event: "ended"}); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	rdb.AddHook(rec)
	a := e.Authorize(ctx, call("v1"))
	if !a.Allowed {
		t.Fatalf("call: %+v", a)
	}
	cmds := rec.take()
	if len(cmds) != 1 || cmds[0][0] != "evalsha" {
		t.Fatalf("commands of an ordinary call: %v", cmds)
	}
	c, err := e.calls.Get(ctx, "v1")
	if err != nil || c == nil {
		t.Fatalf("state: %v %v", c, err)
	}
	sec := strconv.FormatInt(at.Unix(), 10)
	keys := []string{"call:v1", "cc:c:1", "cps:c:1:" + sec, "hold:1", "held:1", "heldsum:1", "holders", "cc:all", "cc:n:TR",
		"hold:b0", "held:b0", "heldsum:b0", "cc:t:1", "cps:t:1:" + sec, "expiry",
		"cc:v:1", "cps:v:1:" + sec, "cc:x", "cc:v:2", "cps:v:2:" + sec, "cc:x", "cc:v:3", "cps:v:3:" + sec, "cc:x"}
	got := cmds[0][2:]
	if n, _ := got[0].(int); n != len(keys) {
		t.Fatalf("number of keys: %v, want %d", got[0], len(keys))
	}
	for i, k := range keys {
		if got[1+i] != k {
			t.Fatalf("key %d: %v, want %s", i+1, got[1+i], k)
		}
	}
	args := got[1+len(keys):]
	// Routes are three wide (no client to deliver to). Route limits:
	// connection 1 takes one call; no route is transcoded (-1).
	want := []any{at.Unix(), at.Unix() + int64(c.TTL), "v1", 3, 1, 0, -1, 0, 0, -1, 0, 0, -1,
		0, 0, "b0", int64(0), int64(0), int64(1), 2, 0, nil /* state */, at.UnixMilli(), c.TTL, e.snap.Load().Clients[1].Balance,
		c.held(), e.synced.Load(), c.Reserved, 0}
	if len(args) != len(want) {
		t.Fatalf("arguments: %v, want %d of them", args, len(want))
	}
	for i, w := range want {
		if w != nil && args[i] != w {
			t.Fatalf("argument %d: %#v, want %#v", i+1, args[i], w)
		}
	}

	// Moving on: the state, then the script, which is told the vendors left
	// and that the call leaves the set of the vendor's route (1) and no
	// client's (0).
	rec.take()
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "v1", Event: "failed", SIPCode: 503}); err != nil {
		t.Fatal(err)
	}
	cmds = rec.take()
	if len(cmds) != 2 || cmds[0][0] != "hgetall" || cmds[1][0] != "evalsha" {
		t.Fatalf("commands of moving on: %v", cmds)
	}
	next := cmds[1][2:]
	wantNext := []any{10, "call:v1", "cc:v:1", "cc:x", "cc:c:0", "cc:v:2", "cps:v:2:" + sec, "cc:x", "cc:v:3", "cps:v:3:" + sec, "cc:x",
		at.Unix(), at.Unix() + int64(c.TTL), "v1", 3, 0, 0, -1, 0, 0, -1, 1, 0, at.UnixMilli()}
	if !reflect.DeepEqual(next, wantNext) {
		t.Fatalf("next: %#v\nwant %#v", next, wantNext)
	}
}
