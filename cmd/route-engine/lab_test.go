// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
	"time"

	"softswitch/core/pkg/engine"
	"softswitch/core/pkg/routing"
)

func TestLabDelayOffLeavesAdjustAlone(t *testing.T) {
	if got := labDelay("", nil); got != nil {
		t.Fatal("no delay and no hook must give no hook")
	}
	called := false
	next := func(_ engine.CallInfo, d routing.Decision) routing.Decision { called = true; return d }
	labDelay("", next)(engine.CallInfo{}, routing.Decision{})
	if !called {
		t.Fatal("the hook given must be the hook returned")
	}
}

func TestLabDelayWaitsForItsPrefixOnly(t *testing.T) {
	calls := 0
	next := func(_ engine.CallInfo, d routing.Decision) routing.Decision { calls++; d.Reason = "next"; return d }
	hook := labDelay("8719=60", next)

	start := time.Now()
	d := hook(engine.CallInfo{Dst: "87195551112233"}, routing.Decision{})
	if waited := time.Since(start); waited < 60*time.Millisecond {
		t.Fatalf("waited %v for a delayed prefix, want at least 60ms", waited)
	}
	if d.Reason != "next" {
		t.Fatal("the decision must still go through the next hook")
	}

	start = time.Now()
	hook(engine.CallInfo{Dst: "905551112233"}, routing.Decision{})
	if waited := time.Since(start); waited > 30*time.Millisecond {
		t.Fatalf("waited %v for another number, want no wait", waited)
	}
	if calls != 2 {
		t.Fatalf("next hook called %d times, want 2", calls)
	}

	// Without a next hook the decision is returned as it came.
	d = labDelay("1=1", nil)(engine.CallInfo{Dst: "1"}, routing.Decision{Reason: "kept"})
	if d.Reason != "kept" {
		t.Fatalf("got %q, want the decision unchanged", d.Reason)
	}
}

func TestLabInbound(t *testing.T) {
	if labInbound("") != nil {
		t.Fatal("no spec must give no hook")
	}
	snap := &routing.Snapshot{Trunks: []routing.Trunk{{ID: 3, ClientID: 7, Username: "lab-nat", HA1: "x"}}}
	hook := labInbound("172.30.0.91=lab-nat, 172.30.0.93=nobody")
	if in, ok := hook(engine.CallInfo{SrcIP: "172.30.0.91"}, snap); !ok || in.ClientID != 7 {
		t.Errorf("listed address: %+v %v", in, ok)
	}
	if in, ok := hook(engine.CallInfo{SrcIP: "::ffff:172.30.0.91"}, snap); !ok || in.ClientID != 7 {
		t.Errorf("listed address written as IPv6: %+v %v", in, ok)
	}
	for _, ip := range []string{"172.30.0.92", "172.30.0.93", "not an address"} {
		if in, ok := hook(engine.CallInfo{SrcIP: ip}, snap); ok {
			t.Errorf("%s taken: %+v", ip, in)
		}
	}
}
