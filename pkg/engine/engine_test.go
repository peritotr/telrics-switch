// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
)

type memSink struct {
	mu   sync.Mutex
	recs []CDR
}

func (m *memSink) Publish(r CDR) { m.mu.Lock(); m.recs = append(m.recs, r); m.mu.Unlock() }

func testSnapshot() *routing.Snapshot {
	return &routing.Snapshot{
		Trunks:  []routing.Trunk{{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1"}}},
		Clients: map[int64]routing.Client{1: {ID: 1, Active: true, Balance: 100 * rating.Micro, TariffID: 1, RoutingPlanID: 1, MaxCalls: 2}},
		Tariffs: map[int64]map[string]rating.Rate{
			1: {"90": {PricePerMin: 20_000, InitialSec: 1, IncrementSec: 1}},
			2: {"90": {PricePerMin: 5_000}}, 3: {"90": {PricePerMin: 8_000}}, 4: {"90": {PricePerMin: 12_000}},
		},
		Plans: map[int64]routing.Plan{1: {ID: 1, Policy: "lcr", Entries: map[string][]routing.Entry{
			"90": {{ConnectionID: 1}, {ConnectionID: 2}, {ConnectionID: 3}},
		}}},
		Connections: map[int64]routing.Connection{
			1: {ID: 1, VendorID: 1, Host: "a", Port: 5060, Transport: "udp", VendorTariffID: 2, Enabled: true, MaxCalls: 1},
			2: {ID: 2, VendorID: 2, Host: "b", Port: 5060, Transport: "udp", VendorTariffID: 3, Enabled: true},
			3: {ID: 3, VendorID: 3, Host: "c", Port: 5060, Transport: "udp", VendorTariffID: 4, Enabled: true},
		},
	}
}

// The tests need a Redis they may wipe: `make test-engine` points them at
// database 15 of the lab instance.
func testEngine(t *testing.T) (*Engine, *memSink, *redis.Client) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { rdb.Close() })
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	sink := &memSink{}
	e := New(func(context.Context) (*routing.Snapshot, error) { return testSnapshot(), nil }, NewRedisCalls(rdb), sink)
	if err := e.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, sink, rdb
}

func call(id string) authorizeRequest {
	return authorizeRequest{V: 1, CallID: id, SrcIP: "10.0.0.1", Src: "100", Dst: "905321112233"}
}

func TestFailoverThenAnswer(t *testing.T) {
	e, sink, rdb := testEngine(t)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }

	a := e.Authorize(ctx, call("c1"))
	if !a.Allowed || a.Route.URI != "sip:905321112233@a:5060;transport=udp" || a.MaxDuration != 7200 {
		t.Fatalf("authorize: %+v", a)
	}
	for i, want := range []string{"sip:905321112233@b:5060;transport=udp", "sip:905321112233@c:5060;transport=udp"} {
		clock = clock.Add(time.Second)
		ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "failed", SIPCode: 500 + 3*i})
		if err != nil || ev.Route == nil || ev.Route.URI != want {
			t.Fatalf("failover %d: %+v %v", i, ev, err)
		}
	}
	clock = clock.Add(2 * time.Second)
	for range 2 { // a retransmitted answer must not move the answer time
		if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"}); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(30 * time.Second)
	}
	for range 2 { // and a repeated end is ignored
		if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"}); err != nil || ev.Route != nil {
			t.Fatalf("ended: %+v %v", ev, err)
		}
	}

	if len(sink.recs) != 3 {
		t.Fatalf("records: %+v", sink.recs)
	}
	for i, want := range []struct {
		conn int64
		code int
	}{{1, 500}, {2, 503}, {3, 200}} {
		r := sink.recs[i]
		if r.Leg != i+1 || *r.ConnectionID != want.conn || r.SIPCode != want.code || r.ClientID != 1 || r.Dst != "905321112233" {
			t.Errorf("leg %d: %+v", i+1, r)
		}
	}
	last := sink.recs[2]
	if last.AnswerAt == nil || last.EndAt.Sub(*last.AnswerAt) != 60*time.Second || last.StartAt.Unix() != 1_800_000_002 {
		t.Errorf("answered leg times: %+v", last)
	}
	if keys, _ := rdb.Keys(ctx, "call:*").Result(); len(keys) != 0 {
		t.Errorf("state left behind: %v", keys)
	}
	for _, k := range []string{clientCalls(1), connCalls(1), connCalls(2), connCalls(3)} {
		if n, _ := rdb.ZCard(ctx, k).Result(); n != 0 {
			t.Errorf("%s still counts %d calls", k, n)
		}
	}
}

func TestFinalCodesStopFailover(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()
	for i, code := range []int{486, 487, 603} {
		id := fmt.Sprint("c", i)
		if a := e.Authorize(ctx, call(id)); !a.Allowed {
			t.Fatalf("authorize: %+v", a)
		}
		if ev, _ := e.Event(ctx, eventRequest{V: 1, CallID: id, Event: "failed", SIPCode: code}); ev.Route != nil {
			t.Errorf("%d must end the call, got %+v", code, ev.Route)
		}
		if r := sink.recs[len(sink.recs)-1]; r.SIPCode != code || r.Leg != 1 {
			t.Errorf("record for %d: %+v", code, r)
		}
	}
}

// A call the SIP core could not set up itself (the media relay took no
// offer, ADR-0030) is reported as ended with 503 before any answer: it is
// recorded with that code, is never moved on, even where every failure
// moves on, and keeps nothing in the state store.
func TestEndedBeforeAnswerStopsFailover(t *testing.T) {
	e, sink, rdb := testEngine(t)
	ctx := context.Background()
	snap := testSnapshot()
	c := snap.Connections[1]
	c.FailoverMode = "always"
	snap.Connections[1] = c
	e.snap.Store(snap)
	if a := e.Authorize(ctx, call("c1")); !a.Allowed || a.Route == nil {
		t.Fatalf("authorize: %+v", a)
	}
	if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended", SIPCode: 503}); err != nil || ev.Route != nil {
		t.Fatalf("ended: %+v %v", ev, err)
	}
	if len(sink.recs) != 1 {
		t.Fatalf("records: %+v", sink.recs)
	}
	if r := sink.recs[0]; r.Leg != 1 || r.SIPCode != 503 || *r.ConnectionID != 1 || r.AnswerAt != nil {
		t.Errorf("record: %+v", r)
	}
	if keys, _ := rdb.Keys(ctx, "call:*").Result(); len(keys) != 0 {
		t.Errorf("state left behind: %v", keys)
	}
	for _, k := range []string{clientCalls(1), connCalls(1)} {
		if n, _ := rdb.ZCard(ctx, k).Result(); n != 0 {
			t.Errorf("%s still counts %d calls", k, n)
		}
	}
	if n, _ := rdb.HLen(ctx, "held:1").Result(); n != 0 {
		t.Errorf("money still held for %d calls", n)
	}
}

// An attempt the SIP core could not send (its media relay took no offer,
// ADR-0039) ends the call as any "ended" before an answer does, but the
// vendor it was meant for is not blamed: neither the live view of the
// connection nor OnLeg counts it as that vendor's attempt. Without the
// cause the attempt is the connection's, as before.
func TestEndedMediaCauseSparesConnection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause string
		sent  bool
	}{
		{"media", CauseMedia, false},
		{"no cause", "", true},
		{"unknown cause", "something-newer", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, sink, rdb := testEngine(t)
			ctx := context.Background()
			snap := testSnapshot()
			c := snap.Connections[1]
			c.FailoverMode = "always"
			snap.Connections[1] = c
			e.snap.Store(snap)
			clock := time.Unix(1_800_000_000, 0)
			e.now = func() time.Time { return clock }
			var legs []Leg
			e.OnLeg = func(l Leg) { legs = append(legs, l) }

			if a := e.Authorize(ctx, call("c1")); !a.Allowed || a.Route == nil {
				t.Fatalf("authorize: %+v", a)
			}
			clock = clock.Add(100 * time.Millisecond)
			ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended", SIPCode: 503, Cause: tc.cause})
			if err != nil || ev.Route != nil {
				t.Fatalf("ended: %+v %v", ev, err)
			}
			if len(sink.recs) != 1 {
				t.Fatalf("records: %+v", sink.recs)
			}
			// The record names the route that was chosen either way.
			if r := sink.recs[0]; r.Leg != 1 || r.SIPCode != 503 || r.ConnectionID == nil || *r.ConnectionID != 1 || r.AnswerAt != nil {
				t.Errorf("record: %+v", r)
			}
			if keys, _ := rdb.Keys(ctx, "call:*").Result(); len(keys) != 0 {
				t.Errorf("state left behind: %v", keys)
			}
			for _, k := range []string{clientCalls(1), connCalls(1), expiryKey} {
				if n, _ := rdb.ZCard(ctx, k).Result(); n != 0 {
					t.Errorf("%s still counts %d calls", k, n)
				}
			}
			if n, _ := rdb.HLen(ctx, "held:1").Result(); n != 0 {
				t.Errorf("money still held for %d calls", n)
			}
			if len(legs) != 1 || legs[0].NotSent == tc.sent || !legs[0].Final || legs[0].Record.SIPCode != 503 {
				t.Errorf("OnLeg: %+v", legs)
			}

			clock = clock.Add(time.Second)
			v, err := e.view(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var conn liveConn
			for _, lc := range v.Connections {
				if lc.ConnectionID == 1 {
					conn = lc
				}
			}
			want := tally{}
			if tc.sent {
				want = tally{Attempts: 1, Finished: 1, NetFailed: 1, Busy503: 1}
			}
			if conn.tally != want {
				t.Errorf("connection 1: %+v, want %+v", conn.tally, want)
			}
			// The call itself failed all the same, for the client and in total.
			if got := v.Total; got.Attempts != 1 || got.Finished != 1 || got.NetFailed != 1 || got.Busy503 != 1 {
				t.Errorf("total: %+v", got)
			}
			if len(v.Clients) != 1 || v.Clients[0].Finished != 1 || v.Clients[0].Busy503 != 1 {
				t.Errorf("clients: %+v", v.Clients)
			}
			var tr liveCountry
			for _, lc := range v.Countries {
				if lc.Country == "TR" {
					tr = lc
				}
			}
			if n := len(tr.Connections); (n == 1) != tc.sent {
				t.Errorf("country's connections: %+v", tr.Connections)
			}
			codes := map[string]uint32{}
			for _, s := range v.SIPCodes {
				codes[s.Key] = s.Count
			}
			if codes["503"] != 1 {
				t.Errorf("codes: %v", codes)
			}
		})
	}
}

// After an answer there is no attempt left to spare: the cause changes
// nothing and the leg is the connection's.
func TestEndedMediaCauseAfterAnswer(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()
	var legs []Leg
	e.OnLeg = func(l Leg) { legs = append(legs, l) }
	if a := e.Authorize(ctx, call("c1")); !a.Allowed {
		t.Fatalf("authorize: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended", Cause: CauseMedia})
	if len(sink.recs) != 1 || sink.recs[0].SIPCode != 200 {
		t.Fatalf("records: %+v", sink.recs)
	}
	if len(legs) != 1 || legs[0].NotSent {
		t.Errorf("OnLeg: %+v", legs)
	}
}

func TestLimits(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()

	// Connection 1 takes one call; the second goes to the next vendor.
	if a := e.Authorize(ctx, call("c1")); a.Route == nil || a.Route.URI != "sip:905321112233@a:5060;transport=udp" {
		t.Fatalf("first: %+v", a)
	}
	if a := e.Authorize(ctx, call("c2")); a.Route == nil || a.Route.URI != "sip:905321112233@b:5060;transport=udp" {
		t.Fatalf("second: %+v", a)
	}
	// The client may hold two calls.
	a := e.Authorize(ctx, call("c3"))
	if a.Allowed || a.SIPCode != 503 || a.Reason != "call limit reached" {
		t.Fatalf("third: %+v", a)
	}
	if r := sink.recs[len(sink.recs)-1]; r.CallID != "c3" || r.SIPCode != 503 || r.ConnectionID != nil {
		t.Errorf("refusal record: %+v", r)
	}
	// Ending one frees both the client's and the connection's place.
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"})
	if a := e.Authorize(ctx, call("c4")); a.Route == nil || a.Route.URI != "sip:905321112233@a:5060;transport=udp" {
		t.Fatalf("after release: %+v", a)
	}

	// A call whose end is never reported stops counting once it must be over.
	clock := time.Now().Add(3 * time.Hour)
	e.now = func() time.Time { return clock }
	if a := e.Authorize(ctx, call("c5")); !a.Allowed {
		t.Fatalf("after expiry: %+v", a)
	}
}

func TestCPSLimit(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	s := testSnapshot()
	c := s.Clients[1]
	c.MaxCalls, c.MaxCPS = 0, 3
	s.Clients[1] = c
	e.snap.Store(s)
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }
	for i := range 5 {
		a := e.Authorize(ctx, call(fmt.Sprint("c", i)))
		if want := i < 3; a.Allowed != want {
			t.Errorf("call %d: %+v", i, a)
		}
	}
	clock = clock.Add(time.Second)
	if a := e.Authorize(ctx, call("next-second")); !a.Allowed {
		t.Errorf("next second: %+v", a)
	}
}

func TestUnknownSourceLeavesNoRecord(t *testing.T) {
	e, sink, _ := testEngine(t)
	in := call("c1")
	in.SrcIP = "10.9.9.9"
	if a := e.Authorize(context.Background(), in); a.Allowed || a.SIPCode != 403 {
		t.Fatalf("%+v", a)
	}
	if len(sink.recs) != 0 {
		t.Errorf("records: %+v", sink.recs)
	}
}

func fundsSnapshot(balance int64, bundles ...routing.Bundle) *routing.Snapshot {
	s := testSnapshot()
	c := s.Clients[1]
	c.Balance, c.MaxCalls = balance, 0
	s.Clients[1] = c
	s.Tariffs[1]["90"] = rating.Rate{PricePerMin: 60_000, InitialSec: 1, IncrementSec: 1}
	conn := s.Connections[1]
	conn.MaxCalls = 0
	s.Connections[1] = conn
	if len(bundles) > 0 {
		s.Bundles = map[int64][]routing.Bundle{1: bundles}
	}
	return s
}

// fundsEngine sells at 1000 micro-units a second to a client holding balance.
func fundsEngine(t *testing.T, balance int64) (*Engine, *memSink, *[]string) {
	e, sink, _ := testEngine(t)
	e.snap.Store(fundsSnapshot(balance))
	var hungUp []string
	e.Hangup = func(_ context.Context, id string) error { hungUp = append(hungUp, id); return nil }
	return e, sink, &hungUp
}

func TestCallsShareTheBalance(t *testing.T) {
	e, _, _ := fundsEngine(t, 90_000) // ninety seconds in all
	ctx := context.Background()

	if a := e.Authorize(ctx, call("c1")); !a.Allowed || a.MaxDuration != 90 {
		t.Fatalf("first: %+v", a)
	}
	// The first call holds a minute; half a minute is left for the second.
	if a := e.Authorize(ctx, call("c2")); !a.Allowed || a.MaxDuration != 30 {
		t.Fatalf("second: %+v", a)
	}
	if a := e.Authorize(ctx, call("c3")); a.Allowed || a.SIPCode != 402 {
		t.Fatalf("third: %+v", a)
	}
	// An unanswered call gives everything back.
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "failed", SIPCode: 486})
	if a := e.Authorize(ctx, call("c4")); !a.Allowed || a.MaxDuration != 60 {
		t.Fatalf("after release: %+v", a)
	}
}

func TestReservationIsExtendedThenCut(t *testing.T) {
	e, sink, hungUp := fundsEngine(t, 150_000)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }
	at := func(sec float64) {
		clock = time.Unix(1_800_000_000, 0).Add(time.Duration(sec * float64(time.Second)))
		if _, err := e.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reserved := func() (int, bool) {
		c, err := e.calls.Get(ctx, "c1")
		if err != nil || c == nil {
			t.Fatalf("call state: %+v %v", c, err)
		}
		return c.Reserved, c.Final
	}

	e.Authorize(ctx, call("c1"))
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	at(49)
	if r, _ := reserved(); r != 60 {
		t.Fatalf("extended early: %d", r)
	}
	at(50) // ten seconds before the first minute runs out
	if r, final := reserved(); r != 120 || final {
		t.Fatalf("first extension: %d %v", r, final)
	}
	at(110) // only thirty more seconds can be paid for
	if r, final := reserved(); r != 150 || !final {
		t.Fatalf("last extension: %d %v", r, final)
	}
	at(148.9)
	if len(*hungUp) != 0 {
		t.Fatalf("cut early: %v", *hungUp)
	}
	at(149)
	if len(*hungUp) != 1 || (*hungUp)[0] != "c1" {
		t.Fatalf("not cut: %v", *hungUp)
	}
	clock = clock.Add(300 * time.Millisecond)
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"})
	if r := sink.recs[0]; r.EndAt.Sub(*r.AnswerAt) > 150*time.Second {
		t.Errorf("billed past the balance: %+v", r)
	}
	// The charge stays held until the balance shows it.
	if a := e.Authorize(ctx, call("c2")); a.Allowed || a.SIPCode != 402 {
		t.Fatalf("spent balance: %+v", a)
	}
}

func TestSettledChargeIsLetGo(t *testing.T) {
	e, _, _ := fundsEngine(t, 150_000)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }

	e.Authorize(ctx, call("c1"))
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	clock = clock.Add(30 * time.Second)
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"})

	// Thirty seconds were used; the snapshot still shows the old balance.
	if a := e.Authorize(ctx, call("c2")); a.MaxDuration != 120 {
		t.Fatalf("before settlement: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "c2", Event: "failed", SIPCode: 486})

	// The rater charges the call, then a snapshot is loaded.
	rdb := e.calls.(*RedisCalls).rdb
	if err := NewSettler(rdb).Settled(ctx, map[int64][]string{1: {"c1", "unknown"}}); err != nil {
		t.Fatal(err)
	}
	if a := e.Authorize(ctx, call("c3")); a.MaxDuration != 120 {
		t.Fatalf("settled but not yet loaded: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "c3", Event: "failed", SIPCode: 486})

	now, _ := e.calls.Clock(ctx)
	s := *e.snap.Load()
	s.Clients = map[int64]routing.Client{1: s.Clients[1]}
	c := s.Clients[1]
	c.Balance = 120_000
	s.Clients[1] = c
	e.snap.Store(&s)
	e.synced.Store(now.UnixMilli() + 1)
	if a := e.Authorize(ctx, call("c4")); a.MaxDuration != 120 {
		t.Fatalf("charge counted twice: %+v", a)
	}
	if n, _ := rdb.Get(ctx, holdKeys(1)[2]).Int64(); n != 60_000 {
		t.Errorf("held %d, want only the minute of the call in progress", n)
	}
}

func TestChangeReloadsAtOnce(t *testing.T) {
	e, _, _ := testEngine(t)
	loads := make(chan struct{}, 10)
	e.load = func(context.Context) (*routing.Snapshot, error) {
		loads <- struct{}{}
		return testSnapshot(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := make(chan struct{}, 1)
	go e.Run(ctx, time.Hour, changes)

	changes <- struct{}{}
	select {
	case <-loads:
	case <-time.After(2 * time.Second):
		t.Fatal("no reload after a change")
	}
	select {
	case <-loads:
		t.Fatal("reloaded without a change")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestLiveView(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }

	e.Authorize(ctx, call("c1"))
	clock = clock.Add(400 * time.Millisecond)
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "failed", SIPCode: 500})
	clock = clock.Add(100 * time.Millisecond)
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ringing"})
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ringing"}) // only the first counts
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	bad := call("c2")
	bad.Dst = "445551112233" // not in the tariff
	e.Authorize(ctx, bad)

	list, total, err := e.calls.List(ctx, clock, 1, 0, 10)
	if err != nil || total != 1 || len(list) != 1 || list[0].CallID != "c1" || list[0].ConnectionID != 2 ||
		list[0].Country != "TR" || list[0].AnswerAt == nil || list[0].RingAt == nil || list[0].Leg != 2 {
		t.Fatalf("list: %+v %d %v", list, total, err)
	}

	clock = clock.Add(20 * time.Second)
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"})
	e.Authorize(ctx, call("c3")) // still in progress when the view is taken
	clock = clock.Add(time.Second)
	v, err := e.view(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Calls != 1 || len(v.Seconds) != liveWindow {
		t.Errorf("calls %d, seconds %d", v.Calls, len(v.Seconds))
	}
	if got, want := v.Total, (tally{Attempts: 3, Finished: 2, Answered: 1, DurationS: 20, PDDMs: 500, PDDSamples: 1}); got != want {
		t.Errorf("total: %+v", got)
	}
	byConn := map[int64]liveConn{}
	for _, c := range v.Connections {
		byConn[c.ConnectionID] = c
	}
	if c := byConn[1]; c.Attempts != 1 || c.NetFailed != 1 || c.Answered != 0 || c.Calls != 1 || c.MaxCalls != 1 {
		t.Errorf("connection 1: %+v", c)
	}
	if c := byConn[2]; c.Attempts != 1 || c.Answered != 1 || c.PDDMs != 100 || c.Calls != 0 {
		t.Errorf("connection 2: %+v", c)
	}
	if len(v.Clients) != 1 || v.Clients[0].Calls != 1 || v.Clients[0].Attempts != 3 || v.Clients[0].MaxCalls != 2 {
		t.Errorf("clients: %+v", v.Clients)
	}
	var tr liveCountry
	for _, c := range v.Countries {
		if c.Country == "TR" {
			tr = c
		}
	}
	if tr.Calls != 1 || tr.Attempts != 2 || tr.Answered != 1 || len(tr.Prefixes) != 1 || tr.Prefixes[0] != (liveShare{"90", 2}) ||
		len(tr.Connections) != 1 || tr.Connections[0] != (liveShare{"2", 1}) {
		t.Errorf("country: %+v", tr)
	}
	codes := map[string]uint32{}
	for _, c := range v.SIPCodes {
		codes[c.Key] = c.Count
	}
	if codes["500"] != 1 || codes["200"] != 1 || codes["403"] != 1 {
		t.Errorf("codes: %v", codes)
	}
}

func TestSystemCallLimit(t *testing.T) {
	e, sink, _ := fundsEngine(t, 100*rating.Micro)
	ctx := context.Background()
	limit := 2
	e.MaxCalls = func() int { return limit }
	for _, id := range []string{"c1", "c2"} {
		if a := e.Authorize(ctx, call(id)); !a.Allowed {
			t.Fatalf("%s: %+v", id, a)
		}
	}
	a := e.Authorize(ctx, call("c3"))
	if a.Allowed || a.SIPCode != 503 || a.Reason != "system capacity reached" {
		t.Fatalf("over the limit: %+v", a)
	}
	if r := sink.recs[len(sink.recs)-1]; r.CallID != "c3" || r.SIPCode != 503 {
		t.Errorf("refusal record: %+v", r)
	}
	// A call ending makes room; so does a higher limit.
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "failed", SIPCode: 486})
	if a := e.Authorize(ctx, call("c4")); !a.Allowed {
		t.Fatalf("after one ended: %+v", a)
	}
	limit = 3
	if a := e.Authorize(ctx, call("c5")); !a.Allowed {
		t.Fatalf("after the limit rose: %+v", a)
	}
}

// The extension points: Adjust may reorder, drop or refuse; OnLeg hears of
// every attempt and of every refused call.
func TestAdjustAndOnLeg(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }
	var legs []Leg
	e.OnLeg = func(l Leg) { legs = append(legs, l) }
	e.Adjust = func(c CallInfo, d routing.Decision) routing.Decision {
		switch c.CallID {
		case "worst-first": // the dearest vendor first, the cheapest left out
			d.Routes = []routing.Route{d.Routes[2], d.Routes[1]}
		case "refused":
			d.Allowed, d.SIPCode, d.Reason = false, 603, "traffic held"
		case "careless": // refused without a code
			d.Allowed = false
		case "emptied":
			d.Routes = nil
		}
		if c.Src != "100" || c.Dst != "905321112233" || c.SrcIP != "10.0.0.1" || !c.Now.Equal(clock) {
			t.Errorf("call info: %+v", c)
		}
		return d
	}

	a := e.Authorize(ctx, call("worst-first"))
	if !a.Allowed || a.Route.URI != "sip:905321112233@c:5060;transport=udp" {
		t.Fatalf("reordered: %+v", a)
	}
	clock = clock.Add(time.Second)
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "worst-first", Event: "ringing", SIPCode: 180}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Second)
	ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "worst-first", Event: "failed", SIPCode: 503})
	if err != nil || ev.Route == nil || ev.Route.URI != "sip:905321112233@b:5060;transport=udp" {
		t.Fatalf("second route: %+v %v", ev, err)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "worst-first", Event: "answered"})
	clock = clock.Add(90 * time.Second)
	if ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "worst-first", Event: "ended"}); err != nil || ev.Route != nil {
		t.Fatalf("ended: %+v %v", ev, err)
	}
	for id, want := range map[string]struct {
		code   int
		reason string
	}{"refused": {603, "traffic held"}, "careless": {403, "not allowed"}, "emptied": {503, "no route"}} {
		if a := e.Authorize(ctx, call(id)); a.Allowed || a.SIPCode != want.code || a.Reason != want.reason {
			t.Errorf("%s: %+v", id, a)
		}
	}

	if len(legs) != 5 || len(sink.recs) != 5 {
		t.Fatalf("legs heard: %d, records: %d", len(legs), len(sink.recs))
	}
	first, second := legs[0], legs[1]
	if *first.Record.ConnectionID != 3 || first.Record.SIPCode != 503 || first.PDD != time.Second || first.Final || first.Talk != 0 || first.SellPerMin != 20_000 {
		t.Errorf("failed attempt: %+v", first)
	}
	if *second.Record.ConnectionID != 2 || second.Record.SIPCode != 200 || !second.Final || second.Talk != 90 || second.PDD != 0 {
		t.Errorf("answered attempt: %+v", second)
	}
	for _, l := range legs[2:] {
		if l.Record.ConnectionID != nil || !l.Final || l.Record.ClientID != 1 {
			t.Errorf("refused call: %+v", l)
		}
	}
}

// bundleEngine is fundsEngine with a bundle of seconds for the client's
// calls, in the period of the test clock.
func bundleEngine(t *testing.T, balance, seconds int64) (*Engine, *memSink, *[]string, func() time.Time, func(float64)) {
	e, sink, hungUp := fundsEngine(t, balance)
	start := time.Unix(1_800_000_000, 0)
	clock := start
	e.now = func() time.Time { return clock }
	e.snap.Store(fundsSnapshot(balance, routing.Bundle{ID: 7, Prefixes: []string{"9053"}, Seconds: seconds, Period: rating.Period(start, nil)}))
	at := func(sec float64) {
		t.Helper()
		clock = start.Add(time.Duration(sec * float64(time.Second)))
		if _, err := e.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return e, sink, hungUp, func() time.Time { return clock }, at
}

// holds reads what a call has reserved and what its ledgers hold in all.
func holds(t *testing.T, e *Engine, id string) (reserved int, bundle, money, bundleSum int64, final bool) {
	t.Helper()
	ctx := context.Background()
	c, err := e.calls.Get(ctx, id)
	if err != nil || c == nil {
		t.Fatalf("call state of %s: %+v %v", id, c, err)
	}
	rdb := e.calls.(*RedisCalls).rdb
	money, _ = rdb.HGet(ctx, holdKeys(1)[1], id).Int64()
	bundleSum, _ = rdb.Get(ctx, ledgerKeys(c.bundleLedger())[2]).Int64()
	return c.Reserved, c.BundleSec, money, bundleSum, c.Final
}

func TestBundleCarriesCallsWithoutBalance(t *testing.T) {
	e, _, _, _, _ := bundleEngine(t, 0, 100)
	ctx := context.Background()

	if a := e.Authorize(ctx, call("c1")); !a.Allowed || a.MaxDuration != 100 {
		t.Fatalf("first: %+v", a)
	}
	if r, b, m, sum, _ := holds(t, e, "c1"); r != 60 || b != 60 || m != 0 || sum != 60 {
		t.Fatalf("first holds %d s, %d of the bundle, %d money, %d in the ledger", r, b, m, sum)
	}
	// Two calls at once cannot both count on the same seconds: the second
	// gets what the first left.
	if a := e.Authorize(ctx, call("c2")); !a.Allowed || a.MaxDuration != 40 {
		t.Fatalf("second: %+v", a)
	}
	if r, b, m, sum, _ := holds(t, e, "c2"); r != 40 || b != 40 || m != 0 || sum != 100 {
		t.Fatalf("second holds %d s, %d of the bundle, %d money, %d in the ledger", r, b, m, sum)
	}
	if a := e.Authorize(ctx, call("c3")); a.Allowed || a.SIPCode != 402 {
		t.Fatalf("third: %+v", a)
	}
	// A number outside the bundle needs money.
	other := call("c4")
	other.Dst = "902161112233"
	if a := e.Authorize(ctx, other); a.Allowed || a.SIPCode != 402 {
		t.Fatalf("outside the bundle: %+v", a)
	}
	// An unanswered call gives its seconds back.
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "failed", SIPCode: 486})
	if a := e.Authorize(ctx, call("c5")); !a.Allowed || a.MaxDuration != 60 {
		t.Fatalf("after release: %+v", a)
	}
}

func TestBundleRunsOutThenBalancePays(t *testing.T) {
	e, sink, hungUp, now, at := bundleEngine(t, 90_000, 90) // ninety seconds each
	ctx := context.Background()

	if a := e.Authorize(ctx, call("c1")); !a.Allowed || a.MaxDuration != 180 {
		t.Fatalf("authorize: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	at(50) // the bundle ends inside the second minute
	if r, b, m, _, final := holds(t, e, "c1"); r != 120 || b != 90 || m != 30_000 || final {
		t.Fatalf("second minute: %d s, %d of the bundle, %d money, final %v", r, b, m, final)
	}
	at(110) // the third minute is the balance's alone
	if r, b, m, _, final := holds(t, e, "c1"); r != 180 || b != 90 || m != 90_000 || final {
		t.Fatalf("third minute: %d s, %d of the bundle, %d money, final %v", r, b, m, final)
	}
	at(170) // nothing is left of either
	if r, _, _, _, final := holds(t, e, "c1"); r != 180 || !final {
		t.Fatalf("after the funds ran out: %d s, final %v", r, final)
	}
	at(179)
	if len(*hungUp) != 1 {
		t.Fatalf("not cut: %v", *hungUp)
	}
	e.now = func() time.Time { return now().Add(300 * time.Millisecond) }
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"})
	if r := sink.recs[0]; r.EndAt.Sub(*r.AnswerAt) > 180*time.Second {
		t.Errorf("ran past what was held: %+v", r)
	}
	// What the call cost stays held, in seconds and in money, until settled.
	rdb := e.calls.(*RedisCalls).rdb
	if n, _ := rdb.Get(ctx, ledgerKeys(BundleLedger(7, rating.Period(now(), nil)))[2]).Int64(); n != 90 {
		t.Errorf("bundle seconds held after the call: %d", n)
	}
	if n, _ := rdb.Get(ctx, holdKeys(1)[2]).Int64(); n != 90_000 {
		t.Errorf("money held after the call: %d", n)
	}
	if a := e.Authorize(ctx, call("c2")); a.Allowed || a.SIPCode != 402 {
		t.Fatalf("spent bundle and balance: %+v", a)
	}
}

func TestBundleRunsOutAndCallIsCut(t *testing.T) {
	e, sink, hungUp, now, at := bundleEngine(t, 0, 90)
	ctx := context.Background()

	e.Authorize(ctx, call("c1"))
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	at(50) // thirty seconds are left of the bundle and there is no balance
	if r, b, m, _, final := holds(t, e, "c1"); r != 90 || b != 90 || m != 0 || !final {
		t.Fatalf("last extension: %d s, %d of the bundle, %d money, final %v", r, b, m, final)
	}
	at(88.9)
	if len(*hungUp) != 0 {
		t.Fatalf("cut early: %v", *hungUp)
	}
	at(89)
	if len(*hungUp) != 1 || (*hungUp)[0] != "c1" {
		t.Fatalf("not cut: %v", *hungUp)
	}
	e.now = func() time.Time { return now().Add(200 * time.Millisecond) }
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"})
	if r := sink.recs[0]; r.EndAt.Sub(*r.AnswerAt) > 90*time.Second {
		t.Errorf("ran past the bundle: %+v", r)
	}
}

func TestBundleSharedByCallsInProgress(t *testing.T) {
	e, _, _, _, at := bundleEngine(t, 60_000, 100)
	ctx := context.Background()

	e.Authorize(ctx, call("c1"))
	e.Authorize(ctx, call("c2")) // forty seconds of the bundle, twenty of the balance
	if r, b, m, sum, _ := holds(t, e, "c2"); r != 60 || b != 40 || m != 20_000 || sum != 100 {
		t.Fatalf("second call: %d s, %d of the bundle, %d money, %d in the ledger", r, b, m, sum)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	at(50) // the first call's next minute finds the bundle taken
	if r, b, m, sum, final := holds(t, e, "c1"); r != 100 || b != 60 || m != 40_000 || sum != 100 || !final {
		t.Fatalf("first call extended: %d s, %d of the bundle, %d money, %d in the ledger, final %v", r, b, m, sum, final)
	}
}

func TestSettledBundleSecondsAreLetGo(t *testing.T) {
	e, _, _, now, at := bundleEngine(t, 0, 100)
	ctx := context.Background()
	period := rating.Period(now(), nil)
	rdb := e.calls.(*RedisCalls).rdb
	sum := func() int64 {
		n, _ := rdb.Get(ctx, ledgerKeys(BundleLedger(7, period))[2]).Int64()
		return n
	}

	e.Authorize(ctx, call("c1"))
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "answered"})
	at(30)
	e.Event(ctx, eventRequest{V: 1, CallID: "c1", Event: "ended"})
	if sum() != 30 {
		t.Fatalf("held after a call of thirty seconds: %d", sum())
	}
	// The snapshot still shows an untouched bundle: the hold stands in.
	if a := e.Authorize(ctx, call("c2")); a.MaxDuration != 70 {
		t.Fatalf("before settlement: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "c2", Event: "failed", SIPCode: 486})

	// The rater counts the seconds, tells the engine, then a snapshot shows them.
	if err := NewSettler(rdb).BundlesSettled(ctx, map[string][]string{BundleLedger(7, period): {"c1", "unknown"}}); err != nil {
		t.Fatal(err)
	}
	if a := e.Authorize(ctx, call("c3")); a.MaxDuration != 70 {
		t.Fatalf("settled but not yet loaded: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "c3", Event: "failed", SIPCode: 486})
	stamp, _ := e.calls.Clock(ctx)
	e.snap.Store(fundsSnapshot(0, routing.Bundle{ID: 7, Prefixes: []string{"9053"}, Seconds: 100, Period: period, Used: 30}))
	e.synced.Store(stamp.UnixMilli() + 1)
	if a := e.Authorize(ctx, call("c4")); a.MaxDuration != 70 {
		t.Fatalf("seconds counted twice: %+v", a)
	}
	if sum() != 60 {
		t.Errorf("held %d, want only the minute of the call in progress", sum())
	}
	// Reloading drops what is settled for bundles nobody calls on, too.
	e.Event(ctx, eventRequest{V: 1, CallID: "c4", Event: "failed", SIPCode: 486})
	if err := e.calls.Purge(ctx, stamp.UnixMilli()+1); err != nil {
		t.Fatal(err)
	}
	if members, _ := rdb.SMembers(ctx, holdersKey).Result(); len(members) != 0 {
		t.Errorf("ledgers left with nothing held: %v", members)
	}
}

func TestBundlePeriodChange(t *testing.T) {
	e, _, _, now, _ := bundleEngine(t, 0, 100)
	ctx := context.Background()
	// The snapshot counted last month's bundle as used up.
	last := rating.Period(now().AddDate(0, -1, 0), nil)
	e.snap.Store(fundsSnapshot(0, routing.Bundle{ID: 7, Prefixes: []string{"9053"}, Seconds: 100, Period: last, Used: 100}))
	e.Authorize(ctx, call("old"))
	if a := e.Authorize(ctx, call("c1")); !a.Allowed || a.MaxDuration != 40 {
		t.Fatalf("new period: %+v", a)
	}
	c, _ := e.calls.Get(ctx, "c1")
	if c.BundlePeriod != rating.Period(now(), nil) || c.BundleSec != 40 {
		t.Errorf("held in period %s: %d s", c.BundlePeriod, c.BundleSec)
	}
	// A call that began in a period that is over takes no more from it.
	e.snap.Store(fundsSnapshot(0, routing.Bundle{ID: 7, Prefixes: []string{"9053"}, Seconds: 100, Period: "2099-01"}))
	if _, bf := e.fundsOf(c); bf.Total != 0 {
		t.Errorf("a period that is over still gives %d s", bf.Total)
	}
}

// A connection's own rule about which answers move a call on, and a
// trunk's own limits.
func TestConnectionRuleAndTrunkLimits(t *testing.T) {
	e, sink, rdb := testEngine(t)
	ctx := context.Background()
	snap := testSnapshot()
	c1, c2 := snap.Connections[1], snap.Connections[2]
	c1.FailoverMode = "always"
	c2.FailoverMode = "never"
	snap.Connections[1], snap.Connections[2] = c1, c2
	e.snap.Store(snap)
	// 486 ends a call by the standing rule; connection 1 moves on all the
	// same, and connection 2 stops at a 503 that would move on.
	if out := e.Authorize(ctx, call("rule")); !out.Allowed || out.Route.URI != "sip:905321112233@a:5060;transport=udp" {
		t.Fatalf("authorize: %+v", out)
	}
	next, err := e.Event(ctx, eventRequest{V: 1, CallID: "rule", Event: "failed", SIPCode: 486})
	if err != nil || next.Route == nil || next.Route.URI != "sip:905321112233@b:5060;transport=udp" {
		t.Fatalf("after 486 on a connection that always moves on: %+v, %v", next, err)
	}
	next, err = e.Event(ctx, eventRequest{V: 1, CallID: "rule", Event: "failed", SIPCode: 503})
	if err != nil || next.Route != nil {
		t.Fatalf("after 503 on a connection that never moves on: %+v, %v", next, err)
	}
	if len(sink.recs) != 2 || sink.recs[1].SIPCode != 503 {
		t.Fatalf("records: %+v", sink.recs)
	}
	if n, _ := rdb.Exists(ctx, callKey("rule")).Result(); n != 0 {
		t.Error("the call's state was left behind")
	}

	// A trunk that may carry one call at a time.
	snap = testSnapshot()
	snap.Trunks[0].MaxCalls = 1
	e.snap.Store(snap)
	if out := e.Authorize(ctx, call("t1")); !out.Allowed {
		t.Fatalf("first call of the trunk: %+v", out)
	}
	if out := e.Authorize(ctx, call("t2")); out.Allowed || out.SIPCode != 503 || out.Reason != "trunk call limit reached" {
		t.Fatalf("second call of the trunk: %+v", out)
	}
	if n, _ := rdb.ZCard(ctx, trunkCalls(1)).Result(); n != 1 {
		t.Errorf("calls counted for the trunk: %d", n)
	}
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "t1", Event: "ended"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := rdb.ZCard(ctx, trunkCalls(1)).Result(); n != 0 {
		t.Errorf("the trunk's count after its call ended: %d", n)
	}
	// A trunk that may place two calls a second: the third is refused.
	snap = testSnapshot()
	snap.Trunks[0].MaxCPS = 2
	c := snap.Clients[1]
	c.MaxCalls = 0
	snap.Clients[1] = c
	e.snap.Store(snap)
	at := time.Now()
	e.now = func() time.Time { return at }
	for _, id := range []string{"s1", "s2"} {
		if out := e.Authorize(ctx, call(id)); !out.Allowed {
			t.Fatalf("call %s of the trunk within the second: %+v", id, out)
		}
	}
	if out := e.Authorize(ctx, call("s3")); out.Allowed || out.SIPCode != 503 || out.Reason != "trunk cps limit reached" {
		t.Fatalf("third call of the trunk within the second: %+v", out)
	}
}
