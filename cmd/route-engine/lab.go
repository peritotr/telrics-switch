// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"softswitch/core/pkg/engine"
	"softswitch/core/pkg/routing"
)

// labDeliver is a hook for the lab, not a feature: LAB_DELIVER lists
// "prefix=user name" pairs, separated by commas, and a call to a number
// that starts with a prefix is delivered to the trunk that signs in under
// that name, wherever it is registered, instead of to a vendor.
//
// The Community edition has no numbers of its own to route to clients;
// what it has is the mechanism: a route that leads to a trunk
// (routing.ToTrunk), which an extension puts into a decision through
// engine.Config.Adjust. This hook does exactly that and nothing else, so
// that the lab's scenarios can place real calls through the mechanism. It
// is off unless the variable is set, and is set nowhere but in the lab.
func labDeliver(spec string, load engine.Loader) (engine.Loader, func(engine.CallInfo, routing.Decision) routing.Decision) {
	if spec == "" {
		return load, nil
	}
	users := map[string]string{} // by prefix
	for _, pair := range strings.Split(spec, ",") {
		prefix, user, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || prefix == "" || user == "" {
			log.Fatalf("LAB_DELIVER: %q is not prefix=user", pair)
		}
		users[prefix] = user
	}
	log.Printf("LAB_DELIVER is set: calls to %d prefixes are delivered to registered trunks. This is a lab hook; do not set it in production", len(users))
	// The hook needs the trunk behind a user name: it keeps the snapshot
	// the engine last loaded.
	var latest atomic.Pointer[routing.Snapshot]
	keep := func(ctx context.Context) (*routing.Snapshot, error) {
		s, err := load(ctx)
		if err == nil {
			latest.Store(s)
		}
		return s, err
	}
	return keep, func(ci engine.CallInfo, d routing.Decision) routing.Decision {
		snap := latest.Load()
		for prefix, user := range users {
			if snap == nil || !strings.HasPrefix(ci.Dst, prefix) {
				continue
			}
			if t, ok := snap.Registrant(user); ok {
				d.Routes = []routing.Route{routing.ToTrunk(t.ID, ci.Dst)}
			} else {
				d.Routes = nil
			}
		}
		return d
	}
}

// labDelay is another hook for the lab, not a feature: LAB_DECIDE_DELAY
// lists "prefix=milliseconds" pairs, and the engine waits that long before
// answering for a call to a number that starts with a prefix. It lets the
// lab's scenarios give up on a call (CANCEL) while the SIP core is still
// waiting for the decision, a moment that is otherwise too short to hit.
// It waits inside Adjust, which runs on the call path: the variable is set
// nowhere but in the lab and must never be set in production.
func labDelay(spec string, next func(engine.CallInfo, routing.Decision) routing.Decision) func(engine.CallInfo, routing.Decision) routing.Decision {
	if spec == "" {
		return next
	}
	waits := map[string]time.Duration{} // by prefix
	for _, pair := range strings.Split(spec, ",") {
		prefix, ms, ok := strings.Cut(strings.TrimSpace(pair), "=")
		n, err := strconv.Atoi(ms)
		if !ok || prefix == "" || err != nil || n <= 0 || n > 5000 {
			log.Fatalf("LAB_DECIDE_DELAY: %q is not prefix=milliseconds (1-5000)", pair)
		}
		waits[prefix] = time.Duration(n) * time.Millisecond
	}
	log.Printf("LAB_DECIDE_DELAY is set: decisions on %d prefixes are delayed. This is a lab hook; do not set it in production", len(waits))
	return func(ci engine.CallInfo, d routing.Decision) routing.Decision {
		for prefix, wait := range waits {
			if strings.HasPrefix(ci.Dst, prefix) {
				time.Sleep(wait)
				break
			}
		}
		if next != nil {
			return next(ci, d)
		}
		return d
	}
}

// labInbound is a third hook for the lab, not a feature: LAB_INBOUND lists
// "address=user name" pairs, and a call from that address that no trunk
// matches is taken on behalf of the client whose trunk signs in under that
// name, instead of being refused as from an unknown source.
//
// The Community edition takes no such calls; what it has is the mechanism,
// engine.Config.Incoming, which an extension uses to take calls a carrier
// hands in for numbers its clients own. This hook uses it in the plainest
// way, so that the lab's scenarios can place real calls through it. Like
// the extension point asks, it decides from memory only: the snapshot it
// is handed. It is off unless the variable is set, and is set nowhere but
// in the lab.
func labInbound(spec string) func(engine.CallInfo, *routing.Snapshot) (routing.Inbound, bool) {
	if spec == "" {
		return nil
	}
	users := map[netip.Addr]string{} // by source address
	for _, pair := range strings.Split(spec, ",") {
		addr, user, ok := strings.Cut(strings.TrimSpace(pair), "=")
		ip, err := netip.ParseAddr(addr)
		if !ok || err != nil || user == "" {
			log.Fatalf("LAB_INBOUND: %q is not address=user", pair)
		}
		users[ip] = user
	}
	log.Printf("LAB_INBOUND is set: calls from %d addresses that no trunk matches are taken for clients. This is a lab hook; do not set it in production", len(users))
	return func(ci engine.CallInfo, snap *routing.Snapshot) (routing.Inbound, bool) {
		ip, err := netip.ParseAddr(ci.SrcIP)
		if err != nil {
			return routing.Inbound{}, false
		}
		user, ok := users[ip.Unmap()]
		if !ok {
			return routing.Inbound{}, false
		}
		t, ok := snap.Registrant(user)
		if !ok {
			return routing.Inbound{}, false
		}
		return routing.Inbound{ClientID: t.ClientID}, true
	}
}
