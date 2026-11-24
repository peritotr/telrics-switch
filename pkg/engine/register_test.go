// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"softswitch/core/pkg/routing"
)

func TestParseContacts(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []contact
		bad  bool
	}{
		{in: `<sip:pbx@10.0.0.5:5060>`, want: []contact{{uri: "sip:pbx@10.0.0.5:5060", expires: -1}}},
		{in: `"Santral, Ana" <sip:pbx@10.0.0.5;transport=udp>;expires=120;q=0.5`, want: []contact{{uri: "sip:pbx@10.0.0.5;transport=udp", expires: 120}}},
		{in: `sip:pbx@10.0.0.5:5062;expires=30`, want: []contact{{uri: "sip:pbx@10.0.0.5:5062", expires: 30}}},
		{in: `Pbx <sips:pbx@example.com> ; Expires = 0 , <sip:b@192.0.2.9>`, want: []contact{{uri: "sips:pbx@example.com", expires: 0}, {uri: "sip:b@192.0.2.9", expires: -1}}},
		{in: `*`, want: []contact{{star: true, expires: -1}}},
		{in: ``, want: nil},
		{in: `<sip:pbx@10.0.0.5`, bad: true},
		{in: `<mailto:a@b>`, bad: true},
		{in: `<sip:>`, bad: true},
		{in: `<sip:a b@c>`, bad: true},
		{in: `<sip:a@b>;expires=soon`, bad: true},
		{in: `<sip:a@b>;expires=-5`, bad: true},
	} {
		got, err := parseContacts(tc.in)
		if tc.bad != (err != nil) || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %+v, %v; want %+v, bad=%v", tc.in, got, err, tc.want, tc.bad)
		}
	}
}

// regSnapshot is the test snapshot with two trunks that sign in: "pbx" of
// client 1 (two contacts allowed) and "secure" that demands TLS.
func regSnapshot() *routing.Snapshot {
	s := testSnapshot()
	s.Trunks = append(s.Trunks,
		routing.Trunk{ID: 7, ClientID: 1, Username: "pbx", HA1: "x", MaxContacts: 2},
		routing.Trunk{ID: 8, ClientID: 1, Username: "secure", HA1: "x", Security: "tls"},
		routing.Trunk{ID: 9, ClientID: 2, Username: "idle", HA1: "x"})
	s.Clients[2] = routing.Client{ID: 2, Active: false}
	s.Firewall, _ = routing.NewFirewall([]string{"198.51.100.66"}, nil)
	return s
}

func regEngine(t *testing.T) (*Engine, *time.Time) {
	e, _, rdb := testEngine(t)
	e.load = func(context.Context) (*routing.Snapshot, error) { return regSnapshot(), nil }
	if err := e.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.Registry = NewRedisRegistry(rdb)
	clock := time.Unix(1_800_000_000, 0)
	e.now = func() time.Time { return clock }
	return e, &clock
}

func reg(user, contacts string, expires int) registerRequest {
	in := registerRequest{V: 1, AuthUser: user, SrcIP: "203.0.113.9", SrcPort: 40123, Transport: "udp", Contacts: contacts, CallID: "r1", CSeq: 1}
	if expires >= 0 {
		in.Expires = &expires
	}
	return in
}

func TestRegisterStoresAndExpires(t *testing.T) {
	e, clock := regEngine(t)
	ctx := context.Background()

	// A trunk with a public address of its own keeps what it asked for,
	// up to the most allowed.
	out := e.Register(ctx, reg("pbx", "<sip:pbx@203.0.113.9:5060>", 7200))
	if !out.Allowed || out.SIPCode != 200 || out.Contacts != "<sip:pbx@203.0.113.9:5060>;expires=3600" {
		t.Fatalf("register = %+v", out)
	}
	list, _ := e.Registry.Of(ctx, 7, *clock)
	if len(list) != 1 || list[0].Contact != "sip:pbx@203.0.113.9:5060" || list[0].SrcIP != "203.0.113.9" || list[0].SrcPort != 40123 ||
		list[0].NAT || list[0].NextHop() != "" || !list[0].Since.Equal(*clock) || !list[0].Until.Equal(clock.Add(time.Hour)) {
		t.Fatalf("binding = %+v", list)
	}
	// Renewed half an hour on: it keeps the moment it first came.
	since := *clock
	*clock = clock.Add(30 * time.Minute)
	in := reg("pbx", "<sip:pbx@203.0.113.9:5060>;expires=600", -1)
	in.CSeq = 2
	if out = e.Register(ctx, in); out.Contacts != "<sip:pbx@203.0.113.9:5060>;expires=600" {
		t.Fatalf("renewal = %+v", out)
	}
	if list, _ = e.Registry.Of(ctx, 7, *clock); len(list) != 1 || !list[0].Since.Equal(since) || !list[0].Until.Equal(clock.Add(10*time.Minute)) {
		t.Fatalf("renewed binding = %+v", list)
	}
	// A request that arrives after a later one of the same series changes nothing.
	stale := reg("pbx", "<sip:pbx@203.0.113.9:5060>;expires=3000", -1)
	if out = e.Register(ctx, stale); out.Contacts != "<sip:pbx@203.0.113.9:5060>;expires=600" {
		t.Fatalf("out-of-order request = %+v", out)
	}
	// A question (no Contact) gets what is in force.
	*clock = clock.Add(9 * time.Minute)
	if out = e.Register(ctx, reg("pbx", "", -1)); !out.Allowed || out.Contacts != "<sip:pbx@203.0.113.9:5060>;expires=60" {
		t.Fatalf("query = %+v", out)
	}
	// Past its time it is gone, from the trunk's list and from the list of all.
	*clock = clock.Add(61 * time.Second)
	if list, _ = e.Registry.Of(ctx, 7, *clock); len(list) != 0 {
		t.Fatalf("after expiry: %+v", list)
	}
	if all, err := e.Registry.All(ctx, *clock); err != nil || len(all) != 0 {
		t.Fatalf("all after expiry: %+v, %v", all, err)
	}
}

func TestRegisterBehindNAT(t *testing.T) {
	e, clock := regEngine(t)
	ctx := context.Background()
	in := reg("pbx", "<sip:pbx@10.0.0.5:5060>", 3600)
	in.NAT = true
	out := e.Register(ctx, in)
	if !out.Allowed || out.Contacts != "<sip:pbx@10.0.0.5:5060>;expires=60" {
		t.Fatalf("register = %+v", out)
	}
	list, _ := e.Registry.Of(ctx, 7, *clock)
	if len(list) != 1 || !list[0].NAT || list[0].NextHop() != "sip:203.0.113.9:40123;transport=udp" {
		t.Fatalf("binding = %+v", list)
	}
	// A connection is a path of its own: TLS counts as "through", NAT or not.
	e.Reg = RegLimits{Min: 60, Max: 3600, NAT: 45}
	tls := reg("pbx", "<sip:pbx@203.0.113.9:5061;transport=tls>", 3600)
	tls.Transport, tls.SrcIP, tls.SrcPort = "TLS", "2001:db8::9", 50111
	*clock = clock.Add(time.Second)
	if out = e.Register(ctx, tls); out.Contacts != "<sip:pbx@203.0.113.9:5061;transport=tls>;expires=45, <sip:pbx@10.0.0.5:5060>;expires=59" {
		t.Fatalf("tls register = %+v", out)
	}
	list, _ = e.Registry.Of(ctx, 7, *clock)
	if list[0].NextHop() != "sip:[2001:db8::9]:50111;transport=tls" {
		t.Fatalf("tls next hop = %q", list[0].NextHop())
	}
	// The shortest a trunk behind NAT may ask for is the NAT interval when
	// that is below the general minimum.
	short := reg("pbx", "<sip:pbx@10.0.0.5:5060>", 45)
	short.NAT = true
	if out = e.Register(ctx, short); !out.Allowed {
		t.Fatalf("45 s behind NAT = %+v", out)
	}
}

func TestRegisterRefusals(t *testing.T) {
	e, clock := regEngine(t)
	ctx := context.Background()
	check := func(what string, in registerRequest, code int, reason string) registerResponse {
		t.Helper()
		out := e.Register(ctx, in)
		if out.Allowed || out.SIPCode != code || out.Reason != reason {
			t.Fatalf("%s: got %+v, want %d %s", what, out, code, reason)
		}
		return out
	}
	if out := check("too brief", reg("pbx", "<sip:a@203.0.113.9>", 20), 423, "interval too brief"); out.MinExpires != 60 {
		t.Fatalf("min expires = %d", out.MinExpires)
	}
	check("unknown user", reg("nobody", "<sip:a@203.0.113.9>", 600), 403, "unknown user")
	check("tls trunk over udp", reg("secure", "<sip:a@203.0.113.9>", 600), 403, "tls required")
	check("inactive account", reg("idle", "<sip:a@203.0.113.9>", 600), 403, "account inactive")
	check("bad contact", reg("pbx", "<tel:+905551112233>", 600), 400, "bad contact: not a SIP address")
	check("star with a lifetime", reg("pbx", "*", 600), 400, "bad contact: * goes alone, with Expires: 0")
	check("more contacts than allowed at once", reg("pbx", "<sip:a@h>, <sip:b@h>, <sip:c@h>", 600), 403, "too many contacts")
	walled := reg("pbx", "<sip:a@198.51.100.66>", 600)
	walled.SrcIP = "198.51.100.66"
	if out := check("blocked address", walled, 403, "source blocked"); !out.SourceBlocked {
		t.Fatal("blocked address not reported as such")
	}
	for _, id := range []int64{7, 8, 9} {
		if list, _ := e.Registry.Of(ctx, id, *clock); len(list) != 0 {
			t.Fatalf("a refused registration was stored: %+v", list)
		}
	}
	// Over TLS the same trunk is taken.
	ok := reg("secure", "<sip:a@203.0.113.9>", 600)
	ok.Transport = "tls"
	if out := e.Register(ctx, ok); !out.Allowed {
		t.Fatalf("tls trunk over tls = %+v", out)
	}
}

func TestRegisterLimitAndRemoval(t *testing.T) {
	e, clock := regEngine(t)
	ctx := context.Background()
	step := func(in registerRequest, want string) {
		t.Helper()
		*clock = clock.Add(time.Second)
		if out := e.Register(ctx, in); !out.Allowed || out.Contacts != want {
			t.Fatalf("got %+v, want contacts %q", out, want)
		}
	}
	// The trunk may have two: a third makes the one renewed longest ago go.
	step(reg("pbx", "<sip:a@203.0.113.9>", 600), "<sip:a@203.0.113.9>;expires=600")
	step(reg("pbx", "<sip:b@203.0.113.9>", 600), "<sip:b@203.0.113.9>;expires=600, <sip:a@203.0.113.9>;expires=599")
	step(reg("pbx", "<sip:c@203.0.113.9>", 600), "<sip:c@203.0.113.9>;expires=600, <sip:b@203.0.113.9>;expires=599")
	// One taken back by the trunk itself, the other left.
	step(reg("pbx", "<sip:b@203.0.113.9>;expires=0", -1), "<sip:c@203.0.113.9>;expires=599")
	// "*" takes back all of them.
	step(reg("pbx", "<sip:d@203.0.113.9>", 600), "<sip:d@203.0.113.9>;expires=600, <sip:c@203.0.113.9>;expires=598")
	step(reg("pbx", "*", 0), "")
	if all, _ := e.Registry.All(ctx, *clock); len(all) != 0 {
		t.Fatalf("left after *: %+v", all)
	}
	// A trunk with the default of one: the newer contact replaces the older.
	tls := func(c string) registerRequest { in := reg("secure", c, 600); in.Transport = "tls"; return in }
	step(tls("<sip:a@203.0.113.9>"), "<sip:a@203.0.113.9>;expires=60")
	step(tls("<sip:b@203.0.113.9>"), "<sip:b@203.0.113.9>;expires=60")
}

func TestRegistrationsOverHTTP(t *testing.T) {
	e, _ := regEngine(t)
	h := e.Handler()
	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := do("POST", "/v1/register", map[string]any{"v": 1, "auth_user": "pbx", "src_ip": "203.0.113.9", "src_port": 5077,
		"transport": "udp", "nat": true, "contacts": "<sip:pbx@10.0.0.5:5060>", "expires": 3600, "call_id": "x", "cseq": 1, "user_agent": "test"})
	if code != 200 || out["allowed"] != true || out["contacts"] != "<sip:pbx@10.0.0.5:5060>;expires=60" {
		t.Fatalf("register: %d %v", code, out)
	}
	// The address is now a registered trunk's: not "known" (its calls still
	// sign in), but no stranger. Another address is neither.
	for ip, want := range map[string]bool{"203.0.113.9": true, "203.0.113.10": false} {
		if code, out = do("POST", "/v1/source", map[string]any{"v": 1, "src_ip": ip}); code != 200 || out["known"] != false || out["registered"] != want {
			t.Fatalf("source %s: %d %v", ip, code, out)
		}
	}
	if code, _ = do("POST", "/v1/register", map[string]any{"v": 2, "auth_user": "pbx"}); code != 400 {
		t.Fatalf("another version: %d", code)
	}
	for _, path := range []string{"/v1/registrations", "/v1/registrations?trunk_id=7"} {
		code, out = do("GET", path, nil)
		list, _ := out["registrations"].([]any)
		if code != 200 || len(list) != 1 {
			t.Fatalf("%s: %d %v", path, code, out)
		}
		b := list[0].(map[string]any)
		if b["trunk_id"] != float64(7) || b["src_ip"] != "203.0.113.9" || b["src_port"] != float64(5077) || b["nat"] != true || b["user_agent"] != "test" {
			t.Fatalf("%s: binding %v", path, b)
		}
	}
	if code, out = do("GET", "/v1/registrations?trunk_id=8", nil); code != 200 || len(out["registrations"].([]any)) != 0 {
		t.Fatalf("another trunk: %d %v", code, out)
	}
	if code, out = do("POST", "/v1/unregister", map[string]any{"v": 1, "trunk_id": 7}); code != 200 || len(out["registrations"].([]any)) != 0 {
		t.Fatalf("unregister: %d %v", code, out)
	}
	if code, out = do("GET", "/v1/registrations", nil); code != 200 || len(out["registrations"].([]any)) != 0 {
		t.Fatalf("after unregister: %d %v", code, out)
	}
	// What was removed stops counting with the next load of the configuration.
	if err := e.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, out = do("POST", "/v1/source", map[string]any{"v": 1, "src_ip": "203.0.113.9"}); code != 200 || out["registered"] != false {
		t.Fatalf("source after removal: %d %v", code, out)
	}
}

// A decision whose route leads to a client's trunk is sent to where that
// trunk has registered from; the leg it makes has no vendor.
func TestCallToRegisteredTrunk(t *testing.T) {
	e, clock := regEngine(t)
	sink := e.sink.(*memSink)
	ctx := context.Background()
	e.Adjust = func(ci CallInfo, d routing.Decision) routing.Decision {
		if ci.Dst == "905321112233" {
			d.Routes = []routing.Route{routing.ToTrunk(7, ci.Dst)}
		}
		return d
	}
	// Not registered: the call has nowhere to go.
	a := e.Authorize(ctx, call("t0"))
	if a.Allowed || a.SIPCode != 480 || a.Reason != "not registered" {
		t.Fatalf("unregistered trunk: %+v", a)
	}
	in := reg("pbx", "<sip:pbx@10.0.0.5:5060>", 3600)
	in.NAT = true
	e.Register(ctx, in)
	*clock = clock.Add(time.Second)
	e.Register(ctx, reg("pbx", "<sip:pbx@203.0.113.9:5060>", 3600))

	// The binding renewed last is tried first; the one behind NAT is
	// reached at the address its registration came from.
	a = e.Authorize(ctx, call("t1"))
	if !a.Allowed || a.Route == nil || a.Route.URI != "sip:pbx@203.0.113.9:5060" || a.Route.NextHop != "" || a.Route.Media != "rtp" {
		t.Fatalf("first route: %+v %+v", a, a.Route)
	}
	ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "t1", Event: "failed", SIPCode: 408})
	if err != nil || ev.Route == nil || ev.Route.URI != "sip:pbx@10.0.0.5:5060" || ev.Route.NextHop != "sip:203.0.113.9:40123;transport=udp" || ev.Route.Media != "rtp" {
		t.Fatalf("second route: %+v %v", ev.Route, err)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "t1", Event: "answered"})
	*clock = clock.Add(5 * time.Second)
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "t1", Event: "ended"}); err != nil {
		t.Fatal(err)
	}
	var legs []CDR
	for _, r := range sink.recs {
		if r.CallID == "t1" {
			legs = append(legs, r)
		}
	}
	if len(legs) != 2 || legs[0].SIPCode != 408 || legs[1].SIPCode != 200 || legs[0].ConnectionID != nil || legs[1].ConnectionID != nil || legs[1].AnswerAt == nil {
		t.Fatalf("legs: %+v", legs)
	}
	for _, l := range legs {
		if l.DstTrunkID == nil || *l.DstTrunkID != 7 {
			t.Fatalf("leg %d does not name the trunk it went to: %v", l.Leg, l.DstTrunkID)
		}
	}
	// Ordinary calls are untouched, and nothing of the call is left behind.
	e.Adjust = nil
	if a = e.Authorize(ctx, call("t2")); !a.Allowed || a.Route.URI != "sip:905321112233@a:5060;transport=udp" || a.Route.NextHop != "" || a.Route.Media != "" {
		t.Fatalf("ordinary call: %+v %+v", a, a.Route)
	}
	e.Event(ctx, eventRequest{V: 1, CallID: "t2", Event: "ended"})
	for _, r := range sink.recs {
		if r.CallID == "t2" && (r.DstTrunkID != nil || r.ConnectionID == nil) {
			t.Fatalf("vendor leg: connection %v, trunk %v", r.ConnectionID, r.DstTrunkID)
		}
	}
	rdb := e.Registry.(*RedisRegistry).rdb
	if n, _ := rdb.Exists(ctx, callKey("t1"), callKey("t2")).Result(); n != 0 {
		t.Fatal("call state left behind")
	}
	if n, _ := rdb.ZCard(ctx, "cc:t:7").Result(); n != 0 {
		t.Fatalf("%d calls still counted on the trunk", n)
	}
	// A trunk that may no longer register (gone from the snapshot) gets no
	// calls though its binding has not run out.
	e.Adjust = func(ci CallInfo, d routing.Decision) routing.Decision {
		d.Routes = []routing.Route{routing.ToTrunk(99, ci.Dst)}
		return d
	}
	if a = e.Authorize(ctx, call("t3")); a.Allowed || a.SIPCode != 480 {
		t.Fatalf("unknown trunk: %+v", a)
	}
}

// The media a registered trunk is offered follows how it registered: over
// TLS, through that connection, nothing is said and the interface's rule
// gives it encrypted audio (SRTP); over UDP it is told plain audio, even
// when its Contact names TLS (ADR-0015: no media keys over plain
// signalling). A TLS attempt that fails moves on to the next binding,
// which gets plain audio again.
func TestRegisteredTrunkMediaByTransport(t *testing.T) {
	e, clock := regEngine(t)
	ctx := context.Background()
	e.Adjust = func(ci CallInfo, d routing.Decision) routing.Decision {
		d.Routes = []routing.Route{routing.ToTrunk(7, ci.Dst)}
		return d
	}
	udp := reg("pbx", "<sip:pbx@203.0.113.9:5061;transport=tls>", 3600)
	if out := e.Register(ctx, udp); !out.Allowed {
		t.Fatalf("udp: %+v", out)
	}
	*clock = clock.Add(time.Second)
	tls := reg("pbx", "<sip:pbx@10.0.0.6:5061;transport=tls>", 3600)
	tls.Transport, tls.SrcPort = "tls", 45001
	if out := e.Register(ctx, tls); !out.Allowed {
		t.Fatalf("tls: %+v", out)
	}

	a := e.Authorize(ctx, call("m1"))
	if !a.Allowed || a.Route == nil || a.Route.URI != "sip:pbx@10.0.0.6:5061;transport=tls" ||
		a.Route.NextHop != "sip:203.0.113.9:45001;transport=tls" || a.Route.Media != "" {
		t.Fatalf("binding over TLS: %+v %+v", a, a.Route)
	}
	// The connection was gone: the attempt failed without an answer.
	ev, err := e.Event(ctx, eventRequest{V: 1, CallID: "m1", Event: "failed", SIPCode: 503})
	if err != nil || ev.Route == nil || ev.Route.URI != "sip:pbx@203.0.113.9:5061;transport=tls" || ev.Route.NextHop != "" || ev.Route.Media != "rtp" {
		t.Fatalf("binding over UDP: %+v %v", ev.Route, err)
	}
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "m1", Event: "ended"}); err != nil {
		t.Fatal(err)
	}
	var got []byte
	got, _ = json.Marshal(e.wire(&Call{}, callRoute{TrunkID: 7, URI: "sip:pbx@10.0.0.6", NextHop: "sip:203.0.113.9:45001;transport=TLS"}))
	if !bytes.Contains(got, []byte(`"next_hop"`)) || bytes.Contains(got, []byte(`"media"`)) {
		t.Fatalf("wire over TLS: %s", got)
	}
	for hop, want := range map[string]string{"": "", "sip:a:1;transport=udp": "udp", "sip:a:1;transport=TLS;x=1": "tls", "sip:a:1": ""} {
		if got := hopTransport(hop); got != want {
			t.Errorf("hopTransport(%q) = %q, want %q", hop, got, want)
		}
	}
}
