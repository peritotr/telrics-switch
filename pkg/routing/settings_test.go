// SPDX-License-Identifier: AGPL-3.0-only

package routing

import (
	"fmt"
	"testing"
)

// rules builds a trunk's number rules from strip/add pairs, sorted as the
// loader sorts them.
func rules(pairs ...string) []NumberRule {
	var out []NumberRule
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, NumberRule{Strip: pairs[i], Add: pairs[i+1]})
	}
	SortNumberRules(out)
	return out
}

// A trunk with one rule turns numbers as the two old settings did.
func TestTrunkNumberFormat(t *testing.T) {
	for _, tc := range []struct{ strip, add, in, want string }{
		{"", "", "905321112233", "905321112233"},
		{"00", "", "00905321112233", "905321112233"},
		{"00", "", "905321112233", "905321112233"}, // not written that way: left alone
		{"+", "", "+905321112233", "905321112233"},
		{"0", "90", "05321112233", "905321112233"},
		{"0", "90", "905321112233", "905321112233"},
		{"", "90", "5321112233", "905321112233"},
	} {
		var r []NumberRule
		if tc.strip != "" || tc.add != "" {
			r = rules(tc.strip, tc.add)
		}
		if got := (Trunk{NumberRules: r}).Dialled(tc.in); got != tc.want {
			t.Errorf("strip %q add %q: %s became %s, want %s", tc.strip, tc.add, tc.in, got, tc.want)
		}
	}
}

// With several rules the longest strip a number starts with wins, in
// whatever order the rules were written; a number none matches is left
// alone, unless a rule with an empty strip catches the rest.
func TestTrunkNumberRules(t *testing.T) {
	many := rules("0", "90", "+", "", "00", "")
	if fmt.Sprint(many) != "[{00 } {+ } {0 90}]" {
		t.Fatalf("sorted rules: %v", many)
	}
	for in, want := range map[string]string{
		"00905321112233": "905321112233", // 00, not 0 → 900905…
		"+905321112233":  "905321112233",
		"05321112233":    "905321112233",
		"905321112233":   "905321112233", // no rule: as it came
	} {
		if got := (Trunk{NumberRules: many}).Dialled(in); got != want {
			t.Errorf("%s became %s, want %s", in, got, want)
		}
	}
	rest := rules("", "90", "00", "")
	for in, want := range map[string]string{"00905321112233": "905321112233", "5321112233": "905321112233"} {
		if got := (Trunk{NumberRules: rest}).Dialled(in); got != want {
			t.Errorf("with a catch-all rule: %s became %s, want %s", in, got, want)
		}
	}
	// A longer strip that is not a prefix of the number does not get in
	// the way of a shorter one that is.
	if got := (Trunk{NumberRules: rules("011", "1", "0", "90")}).Dialled("05321112233"); got != "905321112233" {
		t.Errorf("shorter rule after a longer one that does not match: %s", got)
	}
}

func TestConnectionCallerAndFailover(t *testing.T) {
	c := Connection{CLIStrip: 2, CLIAdd: "+"}
	for in, want := range map[string]string{"902125550000": "+2125550000", "+902125550000": "+2125550000", "anonymous": "anonymous", "9": "+"} {
		if got := c.CallerFor(in); got != want {
			t.Errorf("caller %s shown as %s, want %s", in, got, want)
		}
	}
	if got := (Connection{}).CallerFor("+902125550000"); got != "+902125550000" {
		t.Errorf("a connection without a rule changed the caller to %s", got)
	}
	for _, tc := range []struct {
		mode     string
		codes    []int
		code     int
		standard bool
		want     bool
	}{
		{"", nil, 503, true, true}, {"", nil, 486, false, false}, {"standard", nil, 503, true, true},
		{"always", nil, 486, false, true}, {"always", nil, 603, false, true}, {"always", nil, 487, false, false},
		{"never", nil, 503, true, false}, {"never", nil, 408, true, false},
		{"custom", []int{486, 503}, 486, false, true}, {"custom", []int{486}, 503, true, false}, {"custom", []int{487}, 487, false, false},
	} {
		if got := (Connection{FailoverMode: tc.mode, FailoverCodes: tc.codes}).MovesOn(tc.code, tc.standard); got != tc.want {
			t.Errorf("mode %q %v, code %d: moves on = %v, want %v", tc.mode, tc.codes, tc.code, got, tc.want)
		}
	}
}

func TestCommonCodecs(t *testing.T) {
	for _, tc := range []struct {
		conn, trunk []string
		want        string
		ok          bool
	}{
		{nil, nil, "[]", true},
		{[]string{"PCMA", "PCMU"}, nil, "[PCMA PCMU]", true},
		{nil, []string{"PCMU"}, "[PCMU]", true},
		{[]string{"G729", "PCMA", "PCMU"}, []string{"PCMU", "PCMA"}, "[PCMA PCMU]", true}, // the connection's order
		{[]string{"G729"}, []string{"PCMU"}, "[]", false},
	} {
		got, ok := Connection{Codecs: tc.conn}.CodecsFor(tc.trunk)
		if fmt.Sprint(got) != tc.want || ok != tc.ok {
			t.Errorf("connection %v, trunk %v: %v %v, want %s %v", tc.conn, tc.trunk, got, ok, tc.want, tc.ok)
		}
	}
}

// The settings that shape the decision itself: how the trunk writes
// numbers, the trunk's longest call and limits, the "+" towards a vendor,
// and a connection that shares no codec with the trunk.
func TestSettingsInTheDecision(t *testing.T) {
	s := snapshot()
	s.Trunks[0].NumberRules, s.Trunks[0].MaxDuration = rules("00", ""), 600
	s.Trunks[0].MaxCalls, s.Trunks[0].MaxCPS = 7, 3
	s.Trunks[0].Codecs = []string{"PCMA"}
	c1, c3 := s.Connections[1], s.Connections[3]
	c1.NumberPlus = true
	c3.Codecs = []string{"G729"}
	s.Connections[1], s.Connections[3] = c1, c3
	d := s.Authorize("10.0.0.1", "00905321112233")
	if !d.Allowed || d.Number != "905321112233" || d.MaxDuration != 600 || d.TrunkMaxCalls != 7 || d.TrunkMaxCPS != 3 {
		t.Fatalf("decision: %+v", d)
	}
	var uris []string
	for _, r := range d.Routes {
		uris = append(uris, r.URI)
	}
	want := "[sip:005321112233@v2:5060;transport=udp sip:+905321112233@v1:5060;transport=udp]"
	if fmt.Sprint(uris) != want {
		t.Errorf("routes %v, want %s", uris, want)
	}
	// A trunk without settings decides as before.
	if d := snapshot().Authorize("10.0.0.1", "905321112233"); len(d.Routes) != 2 || d.TrunkMaxCalls != 0 {
		t.Errorf("plain trunk: %+v", d)
	}
}

func TestTranscodes(t *testing.T) {
	for _, tc := range []struct {
		first, second []string
		allowed       bool
		transcode, ok bool
	}{
		{[]string{"PCMU"}, []string{"PCMA"}, false, false, false}, // no codec in common: no route
		{[]string{"PCMU"}, []string{"PCMA"}, true, true, true},    // allowed: translated
		{[]string{"PCMU", "PCMA"}, []string{"PCMA"}, true, false, true},
		{nil, []string{"PCMA"}, true, false, true}, // a side without a list takes what is offered
		{[]string{"PCMU"}, nil, false, false, true},
	} {
		transcode, ok := Transcodes(tc.first, tc.second, tc.allowed)
		if transcode != tc.transcode || ok != tc.ok {
			t.Errorf("%v / %v allowed %v: %v %v, want %v %v", tc.first, tc.second, tc.allowed, transcode, ok, tc.transcode, tc.ok)
		}
	}
}

// A connection and a trunk with no codec in common are no route, unless
// either lets the media relay translate: the route is then kept and
// marked, in its place in the plan. A route with a codec in common is
// never marked.
func TestTranscodingKeepsTheRoute(t *testing.T) {
	setup := func(conn, trunk bool) *Snapshot {
		s := snapshot()
		s.Trunks[0].Codecs, s.Trunks[0].Transcode = []string{"PCMA"}, trunk
		c1 := s.Connections[1]
		c1.Codecs, c1.Transcode = []string{"PCMU"}, conn
		s.Connections[1] = c1
		return s
	}
	marked := func(d Decision) (out []string) {
		for _, r := range d.Routes {
			out = append(out, fmt.Sprintf("%d:%v", r.ConnectionID, r.Transcode))
		}
		return out
	}
	for _, tc := range []struct {
		conn, trunk bool
		want        string
	}{
		{false, false, "[2:false]"},
		{true, false, "[2:false 1:true]"},
		{false, true, "[2:false 1:true]"},
	} {
		d := setup(tc.conn, tc.trunk).Authorize("10.0.0.1", "905321112233")
		if got := fmt.Sprint(marked(d)); !d.Allowed || got != tc.want {
			t.Errorf("connection %v, trunk %v: %s (%+v), want %s", tc.conn, tc.trunk, got, d, tc.want)
		}
	}
}
