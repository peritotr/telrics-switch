// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"testing"

	"softswitch/core/pkg/routing"
)

func TestPrivateID(t *testing.T) {
	for v, want := range map[string]bool{
		"": false, "none": false, "header": false, "user;session": false, "identity": false,
		"id": true, "ID": true, "header;id": true, "id;critical": true, " header ; id ": true, "header, id": true,
	} {
		if got := privateID(v); got != want {
			t.Errorf("privateID(%q) = %v, want %v", v, got, want)
		}
	}
}

// The asserted number reaches the rules on it; without it a call is decided
// as before. A refused caller without a number is recorded with 433.
func TestAuthorizeAsserted(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()
	snap := testSnapshot()
	snap.CLIRules.Add(0, 1, routing.NumberAsserted, "", routing.CLIRule{Action: "anonymous"})
	snap.CLIRules.Add(0, 1, routing.NumberAsserted, "90216", routing.CLIRule{Action: "block"})
	e.snap.Store(snap)

	with := func(id, asserted string) authorizeRequest {
		r := call(id)
		r.Asserted = asserted
		return r
	}
	end := func(id string) {
		if _, err := e.Event(ctx, eventRequest{V: 1, CallID: id, Event: "ended"}); err != nil {
			t.Fatal(err)
		}
	}
	if out := e.Authorize(ctx, with("a1", "902125550000")); !out.Allowed {
		t.Errorf("asserted number no rule refuses: %+v", out)
	}
	end("a1")
	if out := e.Authorize(ctx, with("a2", "902165550000")); out.Allowed || out.SIPCode != 403 {
		t.Errorf("asserted number refused: %+v", out)
	}
	if out := e.Authorize(ctx, call("a3")); out.Allowed || out.SIPCode != 433 || out.Reason != routing.AnonymousCaller {
		t.Errorf("no asserted number: %+v", out)
	}
	if n := len(sink.recs); n != 3 || sink.recs[2].SIPCode != 433 || sink.recs[2].ConnectionID != nil {
		t.Errorf("refusals recorded: %+v", sink.recs)
	}

	// Without rules on the asserted number, the field changes nothing; an
	// older SIP core that never sends it gets what it always got.
	e.snap.Store(testSnapshot())
	for _, id := range []string{"b1", "b2"} {
		r := call(id)
		if id == "b2" {
			r.Asserted = "anonymous"
		}
		if out := e.Authorize(ctx, r); !out.Allowed || out.Route == nil || out.Route.Identity != "" {
			t.Errorf("%s: %+v", id, out)
		}
		end(id)
	}
	var req authorizeRequest
	if err := json.Unmarshal([]byte(`{"v":1,"call_id":"x","src_ip":"10.0.0.1","src":"1","dst":"90"}`), &req); err != nil || req.Asserted != "" || req.Privacy != "" {
		t.Errorf("request without the fields: %+v %v", req, err)
	}
}

// A caller that asks for privacy (Privacy: id) has its asserted identity
// withheld on every route, whatever the connection says, also on the routes
// handed out after a failure.
func TestPrivacyRemovesIdentity(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	snap := testSnapshot()
	c1, c2 := snap.Connections[1], snap.Connections[2]
	c1.PAIMode, c2.PAIMode = "caller", "" // pass
	snap.Connections[1], snap.Connections[2] = c1, c2
	e.snap.Store(snap)

	r := call("p1")
	r.Privacy = "id;critical"
	r.Asserted = "902125550000"
	out := e.Authorize(ctx, r)
	if !out.Allowed || out.Route.Identity != "remove" {
		t.Fatalf("first route of a private call: %+v", out.Route)
	}
	next, err := e.Event(ctx, eventRequest{V: 1, CallID: "p1", Event: "failed", SIPCode: 503})
	if err != nil || next.Route == nil || next.Route.Identity != "remove" {
		t.Fatalf("next route of a private call: %+v %v", next.Route, err)
	}
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "p1", Event: "ended"}); err != nil {
		t.Fatal(err)
	}

	// Without it the connection's setting holds: "caller", then pass.
	r = call("p2")
	r.Privacy = "header"
	if out := e.Authorize(ctx, r); !out.Allowed || out.Route.Identity != "caller" {
		t.Fatalf("first route of a call that keeps its identity: %+v", out.Route)
	}
	next, err = e.Event(ctx, eventRequest{V: 1, CallID: "p2", Event: "failed", SIPCode: 503})
	if err != nil || next.Route == nil || next.Route.Identity != "" {
		t.Fatalf("next route of a call that keeps its identity: %+v %v", next.Route, err)
	}

	// A route to a registered trunk withholds it too.
	w := e.wire(&Call{TrunkID: 1, Private: true}, callRoute{TrunkID: 9, URI: "sip:pbx@10.0.0.5"})
	if w.Identity != "remove" {
		t.Errorf("route to a trunk of a private call: %+v", w)
	}
}
