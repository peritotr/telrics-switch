// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"reflect"
	"testing"

	"softswitch/core/pkg/routing"
)

// transcodeSnapshot: the trunk speaks PCMA only. Connection 1 (cheapest,
// "a") takes PCMU only and lets the media relay translate; connection 2
// ("b") takes PCMU only and does not; connection 3 ("c") has no list.
func transcodeSnapshot(policy string) *routing.Snapshot {
	s := testSnapshot()
	s.Trunks[0].Codecs = []string{"PCMA"}
	cl := s.Clients[1]
	cl.MaxCalls = 0
	s.Clients[1] = cl
	c1, c2 := s.Connections[1], s.Connections[2]
	c1.MaxCalls, c1.Codecs, c1.Transcode = 0, []string{"PCMU"}, true
	c2.Codecs = []string{"PCMU"}
	s.Connections[1], s.Connections[2] = c1, c2
	if policy == "priority" {
		// Connection 3 first: a failed attempt there moves on to 1.
		s.Plans[1] = routing.Plan{ID: 1, Policy: "priority", Entries: map[string][]routing.Entry{
			"90": {{ConnectionID: 3, Priority: 1}, {ConnectionID: 1, Priority: 2}},
		}}
	}
	return s
}

func TestTranscodedRouteAndLimit(t *testing.T) {
	e, _, rdb := testEngine(t)
	ctx := context.Background()
	e.snap.Store(transcodeSnapshot("lcr"))
	e.TranscodeMax = 1
	count := func() int64 { n, _ := rdb.ZCard(ctx, transcodeCalls).Result(); return n }
	full := mTranscodeFull.Value()

	// The first call is translated on connection 1: the vendor is offered
	// its own list, and the SIP core is told the caller's.
	out := e.Authorize(ctx, call("x1"))
	if !out.Allowed || out.Route.URI != "sip:905321112233@a:5060;transport=udp" ||
		!reflect.DeepEqual(out.Route.Codecs, []string{"PCMU"}) || !reflect.DeepEqual(out.Route.Transcode, []string{"PCMA"}) {
		t.Fatalf("first call: %+v %+v", out, out.Route)
	}
	if count() != 1 {
		t.Fatalf("calls transcoded: %d", count())
	}
	// At the limit, connection 1 is passed over; 2 shares no codec and
	// does not translate; 3 takes the call as it is.
	out = e.Authorize(ctx, call("x2"))
	if !out.Allowed || out.Route.URI != "sip:905321112233@c:5060;transport=udp" ||
		!reflect.DeepEqual(out.Route.Codecs, []string{"PCMA"}) || out.Route.Transcode != nil {
		t.Fatalf("second call: %+v %+v", out, out.Route)
	}
	if got := mTranscodeFull.Value() - full; got != 1 {
		t.Errorf("routes passed over for the limit: %d", got)
	}
	for _, id := range []string{"x1", "x2"} {
		if _, err := e.Event(ctx, eventRequest{V: 1, CallID: id, Event: "ended"}); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 0 {
		t.Fatalf("calls transcoded after both ended: %d", count())
	}

	// Moving on to a translated route counts it; at the limit the next
	// call finds no route there.
	e.snap.Store(transcodeSnapshot("priority"))
	if out := e.Authorize(ctx, call("x3")); !out.Allowed || out.Route.URI != "sip:905321112233@c:5060;transport=udp" {
		t.Fatalf("third call: %+v", out)
	}
	next, err := e.Event(ctx, eventRequest{V: 1, CallID: "x3", Event: "failed", SIPCode: 503})
	if err != nil || next.Route == nil || next.Route.URI != "sip:905321112233@a:5060;transport=udp" ||
		!reflect.DeepEqual(next.Route.Transcode, []string{"PCMA"}) || count() != 1 {
		t.Fatalf("third call, next route: %+v, %v, %d transcoded", next.Route, err, count())
	}
	if out := e.Authorize(ctx, call("x4")); !out.Allowed {
		t.Fatalf("fourth call: %+v", out)
	}
	next, err = e.Event(ctx, eventRequest{V: 1, CallID: "x4", Event: "failed", SIPCode: 503})
	if err != nil || next.Route != nil {
		t.Fatalf("fourth call at the limit: %+v, %v", next.Route, err)
	}
	// Leaving a translated route for another frees its place.
	if _, err := e.Event(ctx, eventRequest{V: 1, CallID: "x3", Event: "ended"}); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatalf("calls transcoded after the third ended: %d", count())
	}

	// TRANSCODE_MAX=0: no call is translated.
	e.snap.Store(transcodeSnapshot("lcr"))
	e.TranscodeMax = 0
	if out := e.Authorize(ctx, call("x5")); !out.Allowed || out.Route.URI != "sip:905321112233@c:5060;transport=udp" {
		t.Fatalf("no transcoding allowed: %+v", out)
	}
	if count() != 0 {
		t.Errorf("calls transcoded: %d", count())
	}
}

// A setting changed between admission and the answer to the SIP core:
// a route admitted as translated whose lists now share a codec is carried
// as it is.
func TestTranscodeWireFollowsTheLists(t *testing.T) {
	w := &wireRoute{}
	w.wireCodecs([]string{"PCMU"}, []string{"PCMA"}, true)
	if !reflect.DeepEqual(w.Codecs, []string{"PCMU"}) || !reflect.DeepEqual(w.Transcode, []string{"PCMA"}) {
		t.Errorf("translated: %+v", w)
	}
	w = &wireRoute{}
	w.wireCodecs([]string{"PCMU", "PCMA"}, []string{"PCMA"}, true)
	if !reflect.DeepEqual(w.Codecs, []string{"PCMA"}) || w.Transcode != nil {
		t.Errorf("a codec in common: %+v", w)
	}
	w = &wireRoute{}
	w.wireCodecs([]string{"PCMU"}, []string{"PCMA"}, false)
	if len(w.Codecs) != 0 || w.Transcode != nil {
		t.Errorf("not admitted as translated: %+v", w)
	}
}

func TestTranscodeMaxFromEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
		bad  bool
	}{{"", 0, false}, {"0", -1, false}, {"50", 50, false}, {"-3", 0, true}, {"x", 0, true}} {
		t.Setenv("TRANSCODE_MAX", tc.env)
		got, err := TranscodeMaxFromEnv()
		if got != tc.want || (err != nil) != tc.bad {
			t.Errorf("TRANSCODE_MAX=%q: %d %v", tc.env, got, err)
		}
	}
}
