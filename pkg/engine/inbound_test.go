// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
)

// carrier is an address no trunk of testSnapshot matches.
const carrier = "192.0.2.50"

func fromCarrier(id string) authorizeRequest {
	in := call(id)
	in.SrcIP = carrier
	return in
}

// takeFor is an Incoming hook that takes the carrier's calls for client 1
// as in says, and counts how often it was asked and with which snapshot.
type takeFor struct {
	in    routing.Inbound
	asked int
	snap  *routing.Snapshot
	info  CallInfo
}

func (h *takeFor) hook(ci CallInfo, snap *routing.Snapshot) (routing.Inbound, bool) {
	h.asked++
	h.snap, h.info = snap, ci
	if ci.SrcIP != carrier {
		return routing.Inbound{}, false
	}
	in := h.in
	if in.ClientID == 0 {
		in.ClientID = 1
	}
	return in, true
}

// Without the hook a call from an unknown source is refused as it always
// was: 403, nothing recorded, nothing vouched for.
func TestInboundWithoutHook(t *testing.T) {
	e, sink, rdb := testEngine(t)
	a := e.Authorize(context.Background(), fromCarrier("c1"))
	if a.Allowed || a.SIPCode != 403 || a.Reason != routing.UnknownSource || a.SourceKnown || a.Route != nil {
		t.Fatalf("%+v", a)
	}
	if len(sink.recs) != 0 {
		t.Errorf("records: %+v", sink.recs)
	}
	if n, _ := rdb.Exists(context.Background(), callKey("c1")).Result(); n != 0 {
		t.Error("state stored for a refused call")
	}
}

// A hook that takes nothing, or names a client the snapshot does not have,
// leaves the call unknown.
func TestInboundHookDeclines(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }
	h := &takeFor{}
	e.Incoming = h.hook

	in := fromCarrier("c1")
	in.SrcIP = "192.0.2.51" // not the carrier: declined
	if a := e.Authorize(ctx, in); a.Allowed || a.SIPCode != 403 || a.Reason != routing.UnknownSource || a.SourceKnown {
		t.Fatalf("declined: %+v", a)
	}
	if h.asked != 1 || h.info != (CallInfo{CallID: "c1", Src: "100", SrcIP: "192.0.2.51", Dst: "905321112233", Now: clock}) {
		t.Errorf("hook asked %d times with %+v", h.asked, h.info)
	}
	h.in.ClientID = 99
	if a := e.Authorize(ctx, fromCarrier("c2")); a.Allowed || a.SIPCode != 403 || a.Reason != routing.UnknownSource {
		t.Fatalf("client not in the snapshot: %+v", a)
	}
	if len(sink.recs) != 0 {
		t.Errorf("records: %+v", sink.recs)
	}

	// Calls a trunk matches, and callers that signed in, are not its to take.
	h.asked = 0
	if a := e.Authorize(ctx, call("c3")); !a.Allowed || !a.SourceKnown {
		t.Fatalf("trunk's call: %+v", a)
	}
	signed := fromCarrier("c4")
	signed.AuthUser = "nobody"
	if a := e.Authorize(ctx, signed); a.Allowed || a.Reason != routing.UnknownSource {
		t.Fatalf("signed in as nobody: %+v", a)
	}
	if h.asked != 0 {
		t.Errorf("hook asked %d times about calls not its own", h.asked)
	}
}

// A call the hook takes is its client's: the client's limits apply, shared
// with the calls of its trunks, and its records name the client, no trunk.
func TestInboundTakenForClient(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()
	h := &takeFor{}
	e.Incoming = h.hook
	var legs []Leg
	e.OnLeg = func(l Leg) { legs = append(legs, l) }
	var adjusted routing.Decision
	e.Adjust = func(_ CallInfo, d routing.Decision) routing.Decision { adjusted = d; return d }

	a := e.Authorize(ctx, fromCarrier("in1"))
	if !a.Allowed || a.Route == nil || a.Route.URI != "sip:905321112233@a:5060;transport=udp" {
		t.Fatalf("taken: %+v", a)
	}
	// Taking a call vouches for no address.
	if a.SourceKnown {
		t.Error("source_known for a call the hook took")
	}
	if !adjusted.Inbound || adjusted.ClientID != 1 || adjusted.TrunkID != 0 {
		t.Errorf("Adjust saw %+v", adjusted)
	}
	// Client 1 may hold two calls: one from its trunk, then the third is refused.
	if b := e.Authorize(ctx, call("t1")); !b.Allowed {
		t.Fatalf("trunk's call beside it: %+v", b)
	}
	c := e.Authorize(ctx, fromCarrier("in2"))
	if c.Allowed || c.SIPCode != 503 || c.Reason != "call limit reached" || c.SourceKnown {
		t.Fatalf("over the client's limit: %+v", c)
	}
	refusal := sink.recs[len(sink.recs)-1]
	if refusal.CallID != "in2" || refusal.ClientID != 1 || refusal.TrunkID != nil || refusal.SrcIP != carrier || refusal.SIPCode != 503 {
		t.Errorf("refusal record: %+v", refusal)
	}

	// The taken call is answered and ends: one record, the client's.
	e.Event(ctx, eventRequest{V: 1, CallID: "in1", Event: "answered"})
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "in1", Event: "ended"}); err != nil {
		t.Fatal(err)
	}
	var rec *CDR
	for i := range sink.recs {
		if sink.recs[i].CallID == "in1" {
			rec = &sink.recs[i]
		}
	}
	if rec == nil || rec.ClientID != 1 || rec.TrunkID != nil || rec.SrcIP != carrier || rec.Dst != "905321112233" ||
		rec.SIPCode != 200 || rec.ConnectionID == nil || *rec.ConnectionID != 1 || rec.AnswerAt == nil {
		t.Fatalf("record: %+v", rec)
	}
	if len(legs) != 2 || legs[1].Record.CallID != "in1" || !legs[1].Final {
		t.Errorf("legs heard: %+v", legs)
	}
	// Its place is free again.
	if d := e.Authorize(ctx, fromCarrier("in3")); !d.Allowed {
		t.Fatalf("after the end: %+v", d)
	}
}

// The client's money pays for the calls taken for it, shared with its own.
func TestInboundReservesClientFunds(t *testing.T) {
	e, _, _ := fundsEngine(t, 90_000) // ninety seconds at the tariff's price
	ctx := context.Background()
	h := &takeFor{}
	e.Incoming = h.hook

	if a := e.Authorize(ctx, fromCarrier("in1")); !a.Allowed || a.MaxDuration != 90 {
		t.Fatalf("first: %+v", a)
	}
	if _, _, money, _, _ := holds(t, e, "in1"); money != 60_000 {
		t.Errorf("held for the first call: %d", money)
	}
	if a := e.Authorize(ctx, call("t1")); !a.Allowed || a.MaxDuration != 30 {
		t.Fatalf("trunk's call: %+v", a)
	}
	if a := e.Authorize(ctx, fromCarrier("in2")); a.Allowed || a.SIPCode != 402 {
		t.Fatalf("nothing left: %+v", a)
	}
}

// The hook may set the price and the routes: the reservation follows the
// price, and the client's plan is not asked.
func TestInboundRateAndRoutes(t *testing.T) {
	e, _, _ := fundsEngine(t, 90_000)
	ctx := context.Background()
	own := routing.Route{ConnectionID: 3, VendorID: 3, URI: "sip:905321112233@c:5060;transport=udp", Number: "905321112233", Step: 1, Share: 100}
	h := &takeFor{in: routing.Inbound{
		Number: "905321112233",
		Rate:   &rating.Rate{Prefix: "9053", PricePerMin: 120_000, InitialSec: 1, IncrementSec: 1},
		Routes: []routing.Route{own},
	}}
	e.Incoming = h.hook
	in := fromCarrier("in1")
	in.Dst = "+905321112233" // the hook says which number it is
	a := e.Authorize(ctx, in)
	if !a.Allowed || a.MaxDuration != 45 || a.Route == nil || a.Route.URI != own.URI {
		t.Fatalf("%+v %+v", a, a.Route)
	}
	c, err := e.calls.Get(ctx, "in1")
	if err != nil || c == nil || c.Number != "905321112233" || c.Prefix != "9053" || c.Rate.PricePerMin != 120_000 {
		t.Fatalf("call state: %+v %v", c, err)
	}
	// A price the client cannot pay a second of is refused like any other.
	h.in.Rate = &rating.Rate{PricePerMin: 60_000_000, InitialSec: 60, IncrementSec: 60}
	if b := e.Authorize(ctx, fromCarrier("in2")); b.Allowed || b.SIPCode != 402 {
		t.Fatalf("too dear: %+v", b)
	}
}

// The operator's firewall comes first: a denied address is not offered to
// the hook, and leaves no record.
func TestInboundFirewallFirst(t *testing.T) {
	e, sink, _ := testEngine(t)
	s := testSnapshot()
	s.Firewall, _ = routing.NewFirewall([]string{carrier}, nil)
	e.snap.Store(s)
	h := &takeFor{}
	e.Incoming = h.hook
	a := e.Authorize(context.Background(), fromCarrier("in1"))
	if a.Allowed || a.SIPCode != 403 || !a.SourceBlocked {
		t.Fatalf("%+v", a)
	}
	if h.asked != 0 || len(sink.recs) != 0 {
		t.Errorf("hook asked %d times, records %+v", h.asked, sink.recs)
	}
}

// Every load hands the hook the snapshot in force.
func TestInboundAfterReload(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	h := &takeFor{}
	e.Incoming = h.hook
	balance := int64(100 * rating.Micro)
	e.load = func(context.Context) (*routing.Snapshot, error) {
		s := testSnapshot()
		c := s.Clients[1]
		c.Balance = balance
		s.Clients[1] = c
		return s, nil
	}
	for i, want := range []bool{true, false} {
		if err := e.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		a := e.Authorize(ctx, fromCarrier(fmt.Sprint("in", i)))
		if a.Allowed != want || h.snap != e.snap.Load() {
			t.Fatalf("load %d: %+v (hook had the snapshot in force: %v)", i, a, h.snap == e.snap.Load())
		}
		balance = 0 // the next load leaves the client nothing
	}
}

// A call taken for a client may be delivered to one of its trunks
// (routing.ToTrunk from Adjust): the numbers a client owns reach its PBX.
func TestInboundDeliveredToTrunk(t *testing.T) {
	e, _, _, _ := deliverEngine(t, func(*routing.Trunk) {})
	h := &takeFor{}
	e.Incoming = h.hook
	a := e.Authorize(context.Background(), fromCarrier("in1"))
	if !a.Allowed || a.Route == nil || a.Route.URI != "sip:pbx@10.0.0.5:5060" || a.SourceKnown {
		t.Fatalf("%+v %+v", a, a.Route)
	}
}

// The hook may send the call to a trunk itself, with no Adjust at all.
func TestInboundRoutesToTrunk(t *testing.T) {
	e, _, _, _ := deliverEngine(t, func(*routing.Trunk) {})
	e.Adjust = nil
	h := &takeFor{in: routing.Inbound{Routes: []routing.Route{routing.ToTrunk(7, "905321112233")}}}
	e.Incoming = h.hook
	a := e.Authorize(context.Background(), fromCarrier("in1"))
	if !a.Allowed || a.Route == nil || a.Route.URI != "sip:pbx@10.0.0.5:5060" || a.Route.NextHop == "" {
		t.Fatalf("%+v %+v", a, a.Route)
	}
}
