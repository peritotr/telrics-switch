// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"testing"

	"softswitch/core/pkg/routing"
)

// The SIP core traces a call when the engine says it is a client's
// (ADR-0046): every call it admits and every refusal it records, never a
// call from a source it did not recognise or one the firewall shuts out.
func TestAuthorizeTrace(t *testing.T) {
	e, sink, _ := testEngine(t)
	ctx := context.Background()
	e.load = func(context.Context) (*routing.Snapshot, error) {
		s := testSnapshot()
		s.Trunks = append(s.Trunks, routing.Trunk{ID: 2, ClientID: 1, Username: "pbx", HA1: "x"})
		s.Firewall, _ = routing.NewFirewall([]string{"10.0.0.9"}, nil)
		return s, nil
	}
	if err := e.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	if a := e.Authorize(ctx, call("ok")); !a.Allowed || !a.Trace {
		t.Errorf("admitted: %+v", a)
	}
	noRoute := call("noroute")
	noRoute.Dst = "445551112233"
	if a := e.Authorize(ctx, noRoute); a.Allowed || !a.Trace || !a.SourceKnown {
		t.Errorf("refused, client known by address: %+v", a)
	}
	signed := call("signed")
	signed.AuthUser, signed.SrcIP, signed.Dst = "pbx", "203.0.113.9", "445551112233"
	if a := e.Authorize(ctx, signed); a.Allowed || !a.Trace || a.SourceKnown {
		t.Errorf("refused, client signed in: %+v", a)
	}
	stranger := call("stranger")
	stranger.SrcIP = "192.0.2.1"
	if a := e.Authorize(ctx, stranger); a.Allowed || a.Trace || a.Reason != routing.UnknownSource {
		t.Errorf("stranger: %+v", a)
	}
	walled := call("walled")
	walled.SrcIP = "10.0.0.9"
	if a := e.Authorize(ctx, walled); !a.SourceBlocked || a.Trace {
		t.Errorf("blocked: %+v", a)
	}
	// A traced refusal is a recorded one: its record is what lets the
	// client's users see the trace.
	var recorded []string
	for _, r := range sink.recs {
		recorded = append(recorded, r.CallID)
	}
	if len(recorded) != 2 || recorded[0] != "noroute" || recorded[1] != "signed" {
		t.Errorf("records: %v", recorded)
	}
}
