// SPDX-License-Identifier: AGPL-3.0-only
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"softswitch/core/pkg/routing"
)

func TestCanonIP(t *testing.T) {
	for in, want := range map[string]string{
		"FD30:0:0:0:0:0:0:21": "fd30::21", // as OpenSIPS writes it
		"fd30::21":            "fd30::21",
		"[fd30::21]":          "fd30::21",
		"fe80::1%eth0":        "fe80::1",
		"::ffff:10.0.0.1":     "10.0.0.1",
		"10.0.0.1":            "10.0.0.1",
		"":                    "",
		"no:address":          "no:address",
	} {
		if got := canonIP(in); got != want {
			t.Errorf("canonIP(%q) = %q, want %q", in, got, want)
		}
	}
}

// IPv6 from the SIP core (T-28): a call whose source OpenSIPS writes in
// full and in capitals finds the trunk of that address and is recorded
// with the address in its usual form; a trunk registers over IPv6 with an
// address in brackets in its Contact, is reached there (or, behind NAT, at
// the bracketed address it came from), and its address is a registered
// one whichever way it is written.
func TestIPv6CallsAndRegistrations(t *testing.T) {
	e, clock := regEngine(t)
	sink := e.sink.(*memSink)
	ctx := context.Background()
	e.load = func(context.Context) (*routing.Snapshot, error) {
		s := regSnapshot()
		s.Trunks[0].Addrs = []string{"10.0.0.1", "fd30::21"}
		return s, nil
	}
	if err := e.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	in := call("v6")
	in.SrcIP = "FD30:0:0:0:0:0:0:21"
	if a := e.Authorize(ctx, in); !a.Allowed {
		t.Fatalf("IPv6 caller: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "v6", Event: "ended", SIPCode: 486})
	if len(sink.recs) == 0 || sink.recs[len(sink.recs)-1].SrcIP != "fd30::21" {
		t.Fatalf("records: %+v", sink.recs)
	}

	r := reg("pbx", "<sip:pbx@[fd30::72]:5077>", 600)
	r.SrcIP, r.SrcPort = "FD30:0:0:0:0:0:0:72", 5077
	if out := e.Register(ctx, r); !out.Allowed || out.Contacts != "<sip:pbx@[fd30::72]:5077>;expires=600" {
		t.Fatalf("register over IPv6: %+v", out)
	}
	list, _ := e.Registry.Of(ctx, 7, *clock)
	if len(list) != 1 || list[0].SrcIP != "fd30::72" || list[0].Contact != "sip:pbx@[fd30::72]:5077" || list[0].NextHop() != "" {
		t.Fatalf("binding: %+v", list)
	}
	e.Adjust = func(ci CallInfo, d routing.Decision) routing.Decision {
		d.Routes = []routing.Route{routing.ToTrunk(7, ci.Dst)}
		return d
	}
	if a := e.Authorize(ctx, call("to-v6")); !a.Allowed || a.Route == nil || a.Route.URI != "sip:pbx@[fd30::72]:5077" || a.Route.NextHop != "" {
		t.Fatalf("call to the IPv6 registration: %+v", a)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "to-v6", Event: "ended", SIPCode: 486})

	*clock = clock.Add(time.Second)
	nat := reg("pbx", "<sip:pbx@[fd00::5]:5060>", 600)
	nat.SrcIP, nat.SrcPort, nat.NAT, nat.CSeq = "FD30:0:0:0:0:0:0:73", 40000, true, 2
	nat.CallID = "r2"
	if out := e.Register(ctx, nat); !out.Allowed {
		t.Fatalf("register behind NAT over IPv6: %+v", out)
	}
	list, _ = e.Registry.Of(ctx, 7, *clock)
	if len(list) != 2 || list[0].NextHop() != "sip:[fd30::73]:40000;transport=udp" {
		t.Fatalf("bindings: %+v", list)
	}

	h := e.Handler()
	for ip, want := range map[string]bool{"FD30:0:0:0:0:0:0:72": true, "fd30::72": true, "FD30:0:0:0:0:0:0:74": false} {
		body, _ := json.Marshal(map[string]any{"v": 1, "src_ip": ip})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/source", bytes.NewReader(body)))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 || out["registered"] != want {
			t.Fatalf("source %s: %d %v", ip, rec.Code, out)
		}
	}
}
