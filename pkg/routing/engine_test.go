// SPDX-License-Identifier: AGPL-3.0-only

package routing

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"softswitch/core/pkg/rating"
)

func snapshot() *Snapshot {
	return &Snapshot{
		Trunks:  []Trunk{{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1"}}, {ID: 2, ClientID: 1, Addrs: []string{"10.0.0.1"}, TechPrefix: "777"}},
		Clients: map[int64]Client{1: {ID: 1, Active: true, Balance: 1_000_000, TariffID: 1, RoutingPlanID: 1}},
		Tariffs: map[int64]map[string]rating.Rate{
			1: {"90": {PricePerMin: 20_000, InitialSec: 1, IncrementSec: 1}, "9050": {Blocked: true}},
			2: {"90": {PricePerMin: 15_000}},
			3: {"90": {PricePerMin: 10_000}},
			4: {"90": {PricePerMin: 25_000}},
		},
		Plans: map[int64]Plan{1: {ID: 1, Policy: "lcr", Entries: map[string][]Entry{
			"90": {{ConnectionID: 1, Priority: 1}, {ConnectionID: 2, Priority: 2}, {ConnectionID: 3, Priority: 0}},
		}}},
		Connections: map[int64]Connection{
			1: {ID: 1, VendorID: 1, Host: "v1", Port: 5060, Transport: "udp", VendorTariffID: 2, Enabled: true},
			2: {ID: 2, VendorID: 2, Host: "v2", Port: 5060, Transport: "udp", VendorTariffID: 3, Enabled: true, TechPrefix: "00", StripDigits: 2},
			3: {ID: 3, VendorID: 3, Host: "v3", Port: 5060, Transport: "udp", VendorTariffID: 4, Enabled: true},
		},
	}
}

func TestAuthorizeLCR(t *testing.T) {
	d := snapshot().Authorize("10.0.0.1", "905321112233")
	if !d.Allowed || len(d.Routes) != 2 {
		t.Fatalf("decision: %+v", d)
	}
	// Cheapest first; the vendor priced above the sell rate is dropped.
	if d.Routes[0].ConnectionID != 2 || d.Routes[1].ConnectionID != 1 {
		t.Errorf("order: %+v", d.Routes)
	}
	if d.Routes[0].Number != "005321112233" {
		t.Errorf("number rewrite: %q", d.Routes[0].Number)
	}
	if d.MaxDuration != 3000 {
		t.Errorf("max duration: %d", d.MaxDuration)
	}
}

func TestAuthorizeRejects(t *testing.T) {
	s := snapshot()
	cases := map[string]struct {
		ip, dst string
		code    int
	}{
		"unknown ip": {"10.9.9.9", "905321112233", 403},
		"blocked":    {"10.0.0.1", "905011112233", 403},
		"no sell":    {"10.0.0.1", "441112233", 403},
	}
	for name, c := range cases {
		if d := s.Authorize(c.ip, c.dst); d.Allowed || d.SIPCode != c.code {
			t.Errorf("%s: %+v", name, d)
		}
	}
	c := s.Clients[1]
	c.Balance = 0
	s.Clients[1] = c
	if d := s.Authorize("10.0.0.1", "905321112233"); d.SIPCode != 402 {
		t.Errorf("empty balance: %+v", d)
	}
}

func TestTechPrefixTrunk(t *testing.T) {
	d := snapshot().Authorize("10.0.0.1", "777905321112233")
	if !d.Allowed || d.TrunkID != 2 || d.Number != "905321112233" {
		t.Errorf("decision: %+v", d)
	}
}

func BenchmarkAuthorize(b *testing.B) {
	s := snapshot()
	sell, buy := s.Tariffs[1], s.Tariffs[3]
	for i := 0; i < 1_000_000; i++ {
		p := fmt.Sprintf("%07d", i)
		sell[p] = rating.Rate{PricePerMin: 20_000}
		buy[p] = rating.Rate{PricePerMin: 10_000}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Authorize("10.0.0.1", "905321112233")
	}
}

func TestKnownSource(t *testing.T) {
	s := &Snapshot{
		Trunks: []Trunk{{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1"}}},
		Connections: map[int64]Connection{
			1: {ID: 1, Host: "10.9.0.1", Enabled: true},
			2: {ID: 2, Host: "10.9.0.2"}, // switched off
		},
	}
	for ip, want := range map[string]bool{"10.0.0.1": true, "10.9.0.1": true, "10.9.0.2": false, "203.0.113.9": false, "": false} {
		if got := s.KnownSource(ip); got != want {
			t.Errorf("%q: got %v, want %v", ip, got, want)
		}
	}
}

func TestAuthorizeAsUser(t *testing.T) {
	s := &Snapshot{
		Trunks: []Trunk{
			{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1"}},
			{ID: 2, ClientID: 2, Username: "office", HA1: "abc"},
			{ID: 3, ClientID: 2, Username: "nopass"},
		},
		Clients: map[int64]Client{
			1: {ID: 1, Active: true, Balance: rating.Micro, TariffID: 1, RoutingPlanID: 1},
			2: {ID: 2, Active: true, Balance: rating.Micro, TariffID: 1, RoutingPlanID: 1},
		},
		Tariffs: map[int64]map[string]rating.Rate{1: {"90": {PricePerMin: 20_000}}, 2: {"90": {PricePerMin: 5_000}}},
		Plans:   map[int64]Plan{1: {ID: 1, Policy: "lcr", Entries: map[string][]Entry{"90": {{ConnectionID: 1}}}}},
		Connections: map[int64]Connection{
			1: {ID: 1, VendorID: 1, Host: "a", Port: 5060, Transport: "udp", VendorTariffID: 2, Enabled: true},
		},
	}
	// A verified user is recognised wherever it calls from.
	if d := s.AuthorizeAs("office", "198.51.100.7", "905321112233"); !d.Allowed || d.ClientID != 2 || d.TrunkID != 2 {
		t.Errorf("signed in: %+v", d)
	}
	// Without a verified user the address alone decides, and a user name
	// does not borrow another client's address.
	if d := s.AuthorizeAs("", "198.51.100.7", "905321112233"); d.Allowed || d.SIPCode != 403 {
		t.Errorf("stranger: %+v", d)
	}
	if d := s.AuthorizeAs("ghost", "10.0.0.1", "905321112233"); d.Allowed {
		t.Errorf("unknown user from a known address: %+v", d)
	}
	if ha1, ok := s.Credentials("office"); !ok || ha1 != "abc" {
		t.Errorf("credentials: %q %v", ha1, ok)
	}
	for _, u := range []string{"nopass", "ghost", ""} {
		if _, ok := s.Credentials(u); ok {
			t.Errorf("%q must have no credentials", u)
		}
	}
	if s.KnownSource("") {
		t.Error("a trunk that signs in must not make the empty address known")
	}
}

func TestTrunkSecurity(t *testing.T) {
	s := snapshot()
	s.Trunks = []Trunk{
		{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1"}, TechPrefix: "1", Security: "any"},
		{ID: 2, ClientID: 1, Addrs: []string{"10.0.0.1"}, TechPrefix: "2", Security: "tls"},
		{ID: 3, ClientID: 1, Addrs: []string{"10.0.0.1"}, TechPrefix: "3", Security: "cert", CertName: "pbx.example.com"},
	}
	cases := []struct {
		prefix, transport, name string
		allowed                 bool
	}{
		{"1", "udp", "", true},
		{"2", "udp", "", false},
		{"2", "tcp", "", false},
		{"2", "", "", false}, // a SIP core that does not say is not trusted to be TLS
		{"2", "tls", "", true},
		{"3", "tls", "", false},
		{"3", "tls", "other.example.com", false},
		{"3", "udp", "pbx.example.com", false},
		{"3", "tls", "pbx.example.com", true},
	}
	for _, c := range cases {
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", Transport: c.transport, TLSName: c.name}, c.prefix+"905321112233")
		if d.Allowed != c.allowed || (!c.allowed && (d.SIPCode != 403 || d.TrunkID == 0)) {
			t.Errorf("%+v: %+v", c, d)
		}
	}
	// The simulator asks what would happen without carrying a call.
	if d := s.Authorize("10.0.0.1", "2905321112233"); !d.Allowed {
		t.Errorf("simulator: %+v", d)
	}
}

// weighted is a plan of the given policy with one prefix and a connection
// per weight, all at the same price unless prices says otherwise.
func weighted(policy string, weights, priorities []int, prices []int64) *Snapshot {
	s := &Snapshot{
		Trunks:      []Trunk{{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1"}}},
		Clients:     map[int64]Client{1: {ID: 1, Active: true, Balance: 1_000_000_000, TariffID: 1, RoutingPlanID: 1}},
		Tariffs:     map[int64]map[string]rating.Rate{1: {"90": {PricePerMin: 50_000, IncrementSec: 1}}},
		Connections: map[int64]Connection{},
	}
	var entries []Entry
	for i, w := range weights {
		id := int64(i + 1)
		price := int64(10_000)
		if prices != nil {
			price = prices[i]
		}
		s.Tariffs[100+id] = map[string]rating.Rate{"90": {PricePerMin: price, IncrementSec: 1}}
		s.Connections[id] = Connection{ID: id, VendorID: id, Host: "10.9.0.1", Port: 5060, Transport: "udp", VendorTariffID: 100 + id, Enabled: true}
		e := Entry{ConnectionID: id, Priority: 10, Weight: w}
		if priorities != nil {
			e.Priority = priorities[i]
		}
		entries = append(entries, e)
	}
	s.Plans = map[int64]Plan{1: {ID: 1, Policy: policy, Entries: map[string][]Entry{"90": entries}}}
	return s
}

func firsts(s *Snapshot, n int) map[int64]int {
	got := map[int64]int{}
	for i := 0; i < n; i++ {
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Pick: PickFor(fmt.Sprintf("call-%d@host", i))}, "905321112233")
		got[d.Routes[0].ConnectionID]++
	}
	return got
}

func TestWeightedDistribution(t *testing.T) {
	const n = 100_000
	near := func(what string, got, want int) {
		t.Helper()
		if d := got - want; d < -n/100 || d > n/100 {
			t.Errorf("%s: got %d, want about %d", what, got, want)
		}
	}
	for _, policy := range []string{"priority", "lcr"} {
		// 70 / 20 / 10, and a route of weight zero that only backs the others.
		s := weighted(policy, []int{70, 20, 10, 0}, nil, nil)
		got := firsts(s, n)
		near(policy+" 70", got[1], n*70/100)
		near(policy+" 20", got[2], n*20/100)
		near(policy+" 10", got[3], n*10/100)
		if got[4] != 0 {
			t.Errorf("%s: weight zero was tried first %d times", policy, got[4])
		}
	}

	// Every route stays in the list, weight zero last; the same call always
	// gets the same order.
	s := weighted("priority", []int{70, 20, 10, 0}, nil, nil)
	src := Source{IP: "10.0.0.1", AnyTransport: true, Pick: PickFor("abc@host")}
	a, b := s.AuthorizeFrom(src, "905321112233"), s.AuthorizeFrom(src, "905321112233")
	if len(a.Routes) != 4 || a.Routes[3].ConnectionID != 4 {
		t.Errorf("routes: %+v", a.Routes)
	}
	for i := range a.Routes {
		if a.Routes[i] != b.Routes[i] {
			t.Errorf("order changed for the same call: %+v / %+v", a.Routes, b.Routes)
		}
	}

	// The second place follows the weights of what is left: after the 70,
	// the 20 comes before the 10 two times out of three.
	second := map[int64]int{}
	seen := 0
	for i := 0; seen < 30_000; i++ {
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Pick: PickFor(fmt.Sprintf("x-%d", i))}, "905321112233")
		if d.Routes[0].ConnectionID == 1 {
			second[d.Routes[1].ConnectionID]++
			seen++
		}
	}
	if d := second[2] - 20_000; d < -600 || d > 600 {
		t.Errorf("second place: %v", second)
	}
}

func TestWeightsStayWithinStep(t *testing.T) {
	// Priority comes first: the lower step is never tried before the higher.
	s := weighted("priority", []int{1, 100, 1}, []int{10, 20, 10}, nil)
	for i := 0; i < 1000; i++ {
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Pick: PickFor(fmt.Sprint(i))}, "905321112233")
		if d.Routes[2].ConnectionID != 2 {
			t.Fatalf("routes: %+v", d.Routes)
		}
	}
	// In a least-cost plan so does price: weights only share calls among
	// vendors that cost the same.
	s = weighted("lcr", []int{1, 100, 100}, nil, []int64{10_000, 20_000, 20_000})
	got := map[int64]int{}
	for i := 0; i < 1000; i++ {
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Pick: PickFor(fmt.Sprint(i))}, "905321112233")
		if d.Routes[0].ConnectionID != 1 {
			t.Fatalf("routes: %+v", d.Routes)
		}
		got[d.Routes[1].ConnectionID]++
	}
	if got[2] < 400 || got[3] < 400 {
		t.Errorf("equal weights at one price: %v", got)
	}
}

func TestSharesWithoutPick(t *testing.T) {
	// What the simulator shows: a fixed order, heaviest first, with each
	// route's step and share.
	s := weighted("priority", []int{1, 3, 0, 5}, []int{10, 10, 10, 20}, nil)
	d := s.Authorize("10.0.0.1", "905321112233")
	type row struct {
		id          int64
		step, share int
	}
	want := []row{{2, 1, 75}, {1, 1, 25}, {3, 1, 0}, {4, 2, 100}}
	for i, r := range d.Routes {
		if got := (row{r.ConnectionID, r.Step, r.Share}); got != want[i] {
			t.Errorf("route %d: got %+v, want %+v", i, got, want[i])
		}
	}
}

func BenchmarkAuthorizeWeighted(b *testing.B) {
	s := weighted("priority", []int{70, 20, 10, 5, 5, 0}, nil, nil)
	src := Source{IP: "10.0.0.1", AnyTransport: true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		src.Pick = uint64(i) + 1
		s.AuthorizeFrom(src, "905321112233")
	}
}

func TestCLIRules(t *testing.T) {
	s := weighted("priority", []int{1}, nil, nil)
	s.Clients[1] = Client{ID: 1, ResellerID: 7, Active: true, Balance: 1_000_000_000, TariffID: 1, RoutingPlanID: 1}
	s.CLIRules = CLIRules{From: CLIRuleSet{
		ByClient: map[int64]map[string]CLIRule{1: {
			"":      {Action: "block"}, // a list: nothing passes unless a rule says so
			"90212": {Action: "allow"},
			"90850": {Action: "rewrite", StripDigits: 2, AddPrefix: "0"},
			"4420":  {Action: "rewrite", StripDigits: 99, AddPrefix: "44"},
			"90216": {Action: "block"},
		}},
		ByReseller: map[int64]map[string]CLIRule{
			7: {"90212666": {Action: "block"}, "90216": {Action: "allow"}, "1": {Action: "allow"}},
			8: {"": {Action: "allow"}}, // another account's rules play no part
		},
	}}
	for _, c := range []struct {
		caller, shown string
		code          int
	}{
		{"902125550000", "", 200},
		{"+902125550000", "", 200}, // a leading plus is not part of the number
		{"905325550000", "", 403},  // not on the list
		{"", "", 403},              // no number at all
		{"anonymous", "", 403},     // a name is no number
		{"902126665000", "", 403},  // the account's longer prefix wins
		{"902165550000", "", 403},  // same length: the client's own rule wins
		{"12125550000", "", 200},   // the account's rule, more specific than the list
		{"908505550000", "08505550000", 200},
		{"+908505550000", "08505550000", 200},
		{"442071234567", "44", 200}, // stripping past the end leaves the prefix
	} {
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: c.caller}, "905321112233")
		if d.SIPCode != c.code || d.Caller != c.shown {
			t.Errorf("%q: got %d %q (%s), want %d %q", c.caller, d.SIPCode, d.Caller, d.Reason, c.code, c.shown)
		}
		if c.code == 403 && (d.Reason != "caller not allowed" || d.ClientID != 1) {
			t.Errorf("%q: refusal %+v", c.caller, d)
		}
	}
	// A question that names no caller is not judged by the rules.
	if d := s.Authorize("10.0.0.1", "905321112233"); !d.Allowed {
		t.Errorf("without a caller: %+v", d)
	}
	// A rule that changes nothing leaves the caller alone.
	s.CLIRules.From.ByClient[1]["33"] = CLIRule{Action: "rewrite"}
	if d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: "33123"}, "905321112233"); !d.Allowed || d.Caller != "" {
		t.Errorf("empty rewrite: %+v", d)
	}
	// Without rules every caller passes.
	s.CLIRules = CLIRules{}
	if d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: "anonymous"}, "905321112233"); !d.Allowed {
		t.Errorf("no rules: %+v", d)
	}
}

// Rules on the asserted number (P-Asserted-Identity) judge that number and
// not From's; the refusal of callers without a number stands on its own
// and goes before every prefix rule.
func TestCLIRulesAsserted(t *testing.T) {
	s := weighted("priority", []int{1}, nil, nil)
	s.Clients[1] = Client{ID: 1, ResellerID: 7, Active: true, Balance: 1_000_000_000, TariffID: 1, RoutingPlanID: 1}
	var r CLIRules
	r.Add(7, 1, NumberAsserted, "", CLIRule{Action: "block"})
	r.Add(7, 1, NumberAsserted, "90212", CLIRule{Action: "allow"})
	r.Add(7, 1, NumberAsserted, "", CLIRule{Action: "anonymous"})
	r.Add(7, 0, NumberFrom, "90850", CLIRule{Action: "rewrite", StripDigits: 2, AddPrefix: "0"})
	r.Add(7, 0, NumberFrom, "90216", CLIRule{Action: "block"})
	s.CLIRules = r
	for _, c := range []struct {
		from, asserted, shown string
		code                  int
		reason                string
	}{
		{"905325550000", "902125550000", "", 200, "ok"}, // From is not on the list; the asserted number is
		{"anonymous", "+902125550000", "", 200, "ok"},   // a hidden From with an asserted number
		{"902125550000", "905325550000", "", 403, "caller not allowed"},
		{"902125550000", "", "", 433, AnonymousCaller}, // no asserted number at all
		{"902125550000", "anonymous", "", 433, AnonymousCaller},
		{"902125550000", "90-212-555", "", 433, AnonymousCaller},        // separators make no number
		{"902165550000", "902125550000", "", 403, "caller not allowed"}, // From's own rule refuses
		{"908505550000", "902125550000", "08505550000", 200, "ok"},      // From's rule changes From
	} {
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: c.from, Asserted: c.asserted}, "905321112233")
		if d.SIPCode != c.code || d.Reason != c.reason || d.Caller != c.shown {
			t.Errorf("%q/%q: got %d %q %q, want %d %q %q", c.from, c.asserted, d.SIPCode, d.Reason, d.Caller, c.code, c.reason, c.shown)
		}
		if c.code != 200 && d.ClientID != 1 {
			t.Errorf("%q/%q: refusal names no client: %+v", c.from, c.asserted, d)
		}
	}

	// The refusal of callers without a number, on From, for the whole
	// account: it goes before a client's list that would let them pass,
	// and does not touch callers with a number.
	var a CLIRules
	a.Add(7, 0, NumberFrom, "", CLIRule{Action: "anonymous"})
	a.Add(7, 1, NumberFrom, "", CLIRule{Action: "allow"})
	s.CLIRules = a
	for caller, code := range map[string]int{"": 433, "anonymous": 433, "+": 433, "sipp": 433, "+902125550000": 200, "12": 200} {
		if d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: caller}, "905321112233"); d.SIPCode != code {
			t.Errorf("anonymous on From, %q: got %d (%s), want %d", caller, d.SIPCode, d.Reason, code)
		}
	}
	// Another account's refusal plays no part; nor does a question with no caller.
	s.Clients[1] = Client{ID: 1, ResellerID: 8, Active: true, Balance: 1_000_000_000, TariffID: 1, RoutingPlanID: 1}
	if d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: "anonymous"}, "905321112233"); !d.Allowed {
		t.Errorf("another account's rule: %+v", d)
	}
	s.Clients[1] = Client{ID: 1, ResellerID: 7, Active: true, Balance: 1_000_000_000, TariffID: 1, RoutingPlanID: 1}
	if d := s.Authorize("10.0.0.1", "905321112233"); !d.Allowed {
		t.Errorf("without a caller: %+v", d)
	}

	// Without rules on the asserted number, the asserted number changes
	// nothing: a call with it is decided as one without it.
	var f CLIRules
	f.Add(7, 1, NumberFrom, "", CLIRule{Action: "block"})
	f.Add(7, 1, NumberFrom, "90212", CLIRule{Action: "allow"})
	s.CLIRules = f
	for _, asserted := range []string{"", "905325550000", "anonymous"} {
		if d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: "902125550000", Asserted: asserted}, "905321112233"); !d.Allowed {
			t.Errorf("From rules only, asserted %q: %+v", asserted, d)
		}
		if d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, Caller: "905325550000", Asserted: asserted}, "905321112233"); d.SIPCode != 403 {
			t.Errorf("From rules only, asserted %q, From off the list: %+v", asserted, d)
		}
	}
}

func TestRatesAndRoutesByTime(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	night := rating.Window{From: 22 * 60, To: 6 * 60}
	s := weighted("priority", []int{1, 1}, []int{10, 20}, nil)
	// Selling: 90 costs 50 by day and 30 by night (Istanbul time); 9053 has
	// a night rate only, so by day it falls back to 90.
	delete(s.Tariffs[1], "90")
	s.Timed = map[int64]TimedTariff{1: {Loc: ist, Rates: map[string][]rating.Rate{
		"90":   {{PricePerMin: 30_000, IncrementSec: 1, Window: night}, {PricePerMin: 50_000, IncrementSec: 1}},
		"9053": {{PricePerMin: 20_000, IncrementSec: 1, Window: night}},
	}}}
	// Routing: the first connection only by day (plan time: Istanbul).
	p := s.Plans[1]
	p.Loc = ist
	p.Entries["90"][0].Window = rating.Window{From: 6 * 60, To: 22 * 60}
	s.Plans[1] = p

	for when, want := range map[string]struct {
		prefix string
		price  int64
		first  int64
		routes int
	}{
		"2026-11-23T09:00:00Z": {"90", 50_000, 1, 2},   // noon in Istanbul
		"2026-11-23T20:00:00Z": {"9053", 20_000, 2, 1}, // 23:00 in Istanbul
		"2026-11-23T02:59:00Z": {"9053", 20_000, 2, 1}, // 05:59
		"2026-11-23T03:00:00Z": {"90", 50_000, 1, 2},   // 06:00: the window has ended
	} {
		at, _ := time.Parse(time.RFC3339, when)
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, AnyCaller: true, At: at}, "905321112233")
		if !d.Allowed || d.SellPrefix != want.prefix || d.SellPrice != want.price || len(d.Routes) != want.routes || d.Routes[0].ConnectionID != want.first {
			t.Errorf("%s: %+v", when, d)
		}
	}
	// A number outside 9053 by night takes the night rate of 90.
	at, _ := time.Parse(time.RFC3339, "2026-11-23T20:00:00Z")
	if d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, AnyCaller: true, At: at}, "902125550000"); d.SellPrice != 30_000 {
		t.Errorf("night rate of the shorter prefix: %+v", d)
	}
}

// timedSnapshot is the snapshot of TestRatesAndRoutesByTime: by day and by
// night rates in Istanbul time, and a first route by day only.
func timedSnapshot(ist *time.Location, cal rating.Calendar) *Snapshot {
	s := weighted("priority", []int{1, 1}, []int{10, 20}, nil)
	delete(s.Tariffs[1], "90")
	s.Timed = map[int64]TimedTariff{1: {Loc: ist, Cal: cal, Rates: map[string][]rating.Rate{
		"90":   {{PricePerMin: 30_000, IncrementSec: 1, Window: rating.Window{Days: 64}}, {PricePerMin: 50_000, IncrementSec: 1}},
		"9053": {{PricePerMin: 20_000, IncrementSec: 1, Window: rating.Window{From: 22 * 60, To: 6 * 60}}},
	}}}
	p := s.Plans[1]
	p.Loc, p.Cal = ist, cal
	p.Entries["90"][0].Window = rating.Window{Days: 31} // weekdays only
	s.Plans[1] = p
	return s
}

func TestHolidays(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	// Wednesday 15 July 2026 is a holiday, counted as a Sunday: the Sunday
	// rate applies and the weekday-only route is left out, in Istanbul time.
	cal := rating.Calendar{{Year: 2026, Month: time.July, Day: 15}: time.Sunday}
	for _, c := range []struct {
		at     string
		cal    rating.Calendar
		price  int64
		routes int
	}{
		{"2026-07-15T09:00:00Z", cal, 30_000, 1},
		{"2026-07-15T09:00:00Z", nil, 50_000, 2}, // without the calendar, a Wednesday
		{"2026-07-14T21:30:00Z", cal, 30_000, 1}, // already the 15th there
		{"2026-07-15T21:30:00Z", cal, 50_000, 2}, // already the 16th there
		{"2026-07-19T09:00:00Z", cal, 30_000, 1}, // a real Sunday
		{"2026-07-14T09:00:00Z", cal, 50_000, 2}, // the day before
	} {
		s := timedSnapshot(ist, c.cal)
		at, _ := time.Parse(time.RFC3339, c.at)
		d := s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, AnyCaller: true, At: at}, "902125550000")
		if !d.Allowed || d.SellPrice != c.price || len(d.Routes) != c.routes || d.Routes[len(d.Routes)-1].ConnectionID != 2 {
			t.Errorf("%s (calendar %v): %+v", c.at, c.cal != nil, d)
		}
	}
}

// BenchmarkAuthorizeTimed is a call whose price and routes depend on the
// day and hour, with a calendar of holidays.
func BenchmarkAuthorizeTimed(b *testing.B) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		b.Fatal(err)
	}
	cal := rating.Calendar{{Year: 2026, Month: time.January, Day: 1}: time.Sunday, {Year: 2026, Month: time.July, Day: 15}: time.Sunday}
	s := timedSnapshot(ist, cal)
	src := Source{IP: "10.0.0.1", AnyTransport: true, AnyCaller: true, At: time.Date(2026, 7, 7, 23, 0, 0, 0, time.UTC)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.AuthorizeFrom(src, "905321112233")
	}
}

func TestVendorsInOtherCurrencies(t *testing.T) {
	// Three vendors: 0.011 in the account's currency, 0.010 in a currency
	// worth 1.20, and 0.30 in one worth 0.03. Cheapest for the account:
	// the third (0.009), then the first, then the second (0.012).
	s := weighted("lcr", []int{1, 1, 1}, nil, []int64{11_000, 10_000, 300_000})
	for id, fx := range map[int64]int64{2: 1_200_000, 3: 30_000} {
		c := s.Connections[id]
		c.Fx = fx
		s.Connections[id] = c
	}
	d := s.Authorize("10.0.0.1", "905321112233")
	var got []int64
	for _, r := range d.Routes {
		got = append(got, r.ConnectionID)
	}
	if len(got) != 3 || got[0] != 3 || got[1] != 1 || got[2] != 2 || d.Routes[0].BuyBase != 9_000 || d.Routes[0].BuyPrice != 300_000 {
		t.Fatalf("order by the account's currency: %v %+v", got, d.Routes)
	}
	// The client pays 0.0125 in a currency worth 0.80: 0.010 for the
	// account. Without leave to sell at a loss only the vendor at 0.009
	// remains.
	c := s.Clients[1]
	c.Fx = 800_000
	s.Clients[1] = c
	s.Tariffs[1]["90"] = rating.Rate{PricePerMin: 12_500, IncrementSec: 1}
	d = s.Authorize("10.0.0.1", "905321112233")
	if len(d.Routes) != 1 || d.Routes[0].ConnectionID != 3 || d.SellBase != 10_000 || d.SellPrice != 12_500 {
		t.Fatalf("loss check across currencies: %+v", d)
	}
}

func TestBundles(t *testing.T) {
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	s := snapshot()
	c := s.Clients[1]
	c.Balance = 0
	s.Clients[1] = c
	s.Tariffs[1]["44"] = rating.Rate{} // a destination that costs nothing
	s.Plans[1].Entries["44"] = []Entry{{ConnectionID: 2}}
	s.Tariffs[3]["44"] = rating.Rate{}
	s.Bundles = map[int64][]Bundle{1: {
		{ID: 7, Prefixes: []string{"90", "44"}, Seconds: 600, Period: "2026-11", Used: 500},
		{ID: 8, Prefixes: []string{"9053"}, Seconds: 300, Period: "2026-11", Used: 300},
		{ID: 9, Prefixes: []string{"90"}, Seconds: 999, Period: "2026-11"}, // the same prefix, made later
	}}
	call := func(dst string, at time.Time) Decision {
		return s.AuthorizeFrom(Source{IP: "10.0.0.1", AnyTransport: true, AnyCaller: true, At: at}, dst)
	}

	// No balance: the bundle alone carries the call, for what is left of it.
	if d := call("902161112233", at); !d.Allowed || d.BundleID != 7 || d.BundleFree != 100 || d.BundlePeriod != "2026-11" || d.MaxDuration != 100 {
		t.Errorf("bundle with seconds left: %+v", d)
	}
	// The longest prefix decides which bundle, even when it is used up.
	if d := call("905321112233", at); d.Allowed || d.SIPCode != 402 {
		t.Errorf("used-up bundle and no balance: %+v", d)
	}
	// A new period starts untouched, whatever the snapshot counted before.
	if d := call("905321112233", at.AddDate(0, 1, 0)); !d.Allowed || d.BundleID != 8 || d.BundleFree != 300 || d.BundlePeriod != "2026-12" {
		t.Errorf("next period: %+v", d)
	}
	// With a balance the bundle comes first and the balance after it.
	c.Balance = 20_000 // one minute
	s.Clients[1] = c
	if d := call("902161112233", at); d.MaxDuration != 160 {
		t.Errorf("bundle then balance: %+v", d)
	}
	// A call that costs nothing takes nothing.
	if d := call("441112223344", at); !d.Allowed || d.BundleID != 0 {
		t.Errorf("free destination: %+v", d)
	}

	b := Bundle{Seconds: 100, Period: "2026-11", Used: 130}
	for period, want := range map[string]int64{"2026-10": 0, "2026-11": 0, "2026-12": 100} {
		if got := b.Left(period); got != want {
			t.Errorf("Left(%s) = %d, want %d", period, got, want)
		}
	}
}

// sources is the usual snapshot with trunks of five clients, each known by
// other addresses and networks.
func sources() *Snapshot {
	s := snapshot()
	s.Trunks = []Trunk{
		{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1", "10.0.0.2", "2001:db8::1"}},
		{ID: 2, ClientID: 2, Addrs: []string{"10.1.0.0/24", "2001:db8:1::/48"}},
		{ID: 3, ClientID: 3, Addrs: []string{"10.1.0.16/28"}},
		{ID: 4, ClientID: 4, Addrs: []string{"10.1.0.17"}, TechPrefix: "77"},
		{ID: 5, ClientID: 5, Addrs: []string{"10.1.0.0/24"}, TechPrefix: "7"},
		{ID: 6, ClientID: 5, Addrs: []string{"10.1.0.0/24"}, TechPrefix: "77"},
		{ID: 7, ClientID: 1, Addrs: []string{"not an address", "10.2.0.9/24"}}, // host bits are dropped: 10.2.0.0/24
		{ID: 8, ClientID: 2, Username: "office", HA1: "abc", Addrs: []string{"10.3.0.1"}},
	}
	for id := int64(2); id <= 5; id++ {
		c := s.Clients[1]
		c.ID = id
		s.Clients[id] = c
	}
	return s
}

func TestTrunkAddresses(t *testing.T) {
	s := sources()
	cases := []struct {
		src, dst string
		trunk    int64 // 0 = unknown source
	}{
		// Every address of a trunk is that trunk.
		{"10.0.0.1", "905321112233", 1},
		{"10.0.0.2", "905321112233", 1},
		{"10.0.0.3", "905321112233", 0},
		// A network holds its addresses, first and last included, and no more.
		{"10.1.0.0", "905321112233", 2},
		{"10.1.0.200", "905321112233", 2},
		{"10.1.0.255", "905321112233", 2},
		{"10.1.1.0", "905321112233", 0},
		{"10.0.255.255", "905321112233", 0},
		// The most specific network wins: /28 before /24, an address before both.
		{"10.1.0.16", "905321112233", 3},
		{"10.1.0.31", "905321112233", 3},
		{"10.1.0.32", "905321112233", 2},
		{"10.1.0.17", "77905321112233", 4},
		// A more specific trunk whose tech prefix does not fit leaves the
		// call to the wider network.
		{"10.1.0.17", "905321112233", 3},
		// Within one network the longest tech prefix decides, as before;
		// a wider network's longer prefix does not beat a narrower network.
		{"10.1.0.200", "7905321112233", 5},
		{"10.1.0.200", "77905321112233", 6},
		{"10.1.0.20", "77905321112233", 3},
		// Bits past the length of a network are ignored.
		{"10.2.0.200", "905321112233", 7},
		// IPv6, in any spelling; IPv4 written as IPv6 is IPv4.
		{"2001:db8::1", "905321112233", 1},
		{"2001:0db8:0:0:0:0:0:1", "905321112233", 1},
		{"2001:db8::2", "905321112233", 0},
		{"2001:db8:1:ffff::9", "905321112233", 2},
		{"2001:db8:2::9", "905321112233", 0},
		{"::ffff:10.0.0.2", "905321112233", 1},
		// A trunk that signs in is not known by an address.
		{"10.3.0.1", "905321112233", 0},
		{"", "905321112233", 0},
		{"10.0.0.1:5060", "905321112233", 0},
	}
	for _, c := range cases {
		d := s.Authorize(c.src, c.dst)
		if d.TrunkID != c.trunk || (c.trunk == 0 && (d.Allowed || d.Reason != "unknown source")) {
			t.Errorf("%s -> %s: trunk %d (%s), want %d", c.src, c.dst, d.TrunkID, d.Reason, c.trunk)
		}
		if c.trunk != 0 && d.ClientID != s.Trunks[c.trunk-1].ClientID {
			t.Errorf("%s -> %s: client %d", c.src, c.dst, d.ClientID)
		}
		// The flood protection must know exactly the sources that can
		// belong to a trunk, whatever number they dial.
		known := c.trunk != 0
		if got := s.KnownSource(c.src); got != known {
			t.Errorf("KnownSource(%q) = %v, want %v", c.src, got, known)
		}
	}
	// Known by its address even when no tech prefix fits the number.
	s.Trunks = []Trunk{{ID: 1, ClientID: 1, Addrs: []string{"10.5.0.0/16"}, TechPrefix: "55"}}
	s.index, s.trunksBy = sync.Once{}, nil
	s.bits4, s.bits6 = nil, nil
	if d := s.Authorize("10.5.9.9", "905321112233"); d.Reason != "unknown source" || !s.KnownSource("10.5.9.9") {
		t.Errorf("prefix does not fit: %+v, known %v", d, s.KnownSource("10.5.9.9"))
	}
}

// A switch with many customers' networks: the cost of finding the trunk
// depends on how many different network lengths are in use, not on how
// many networks there are.
func BenchmarkAuthorizeNetworks(b *testing.B) {
	s := snapshot()
	s.Trunks = nil
	for i := 0; i < 5000; i++ {
		s.Trunks = append(s.Trunks, Trunk{ID: int64(i + 1), ClientID: 1, Addrs: []string{
			fmt.Sprintf("10.%d.%d.7", i/250, i%250), fmt.Sprintf("172.%d.%d.0/%d", 16+i/250, i%250, 24+i%6), fmt.Sprintf("2001:db8:%x::/48", i),
		}})
	}
	s.Trunks = append(s.Trunks, Trunk{ID: 9999, ClientID: 1, Addrs: []string{"192.168.0.0/16"}})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if d := s.Authorize("192.168.44.5", "905321112233"); !d.Allowed {
			b.Fatal(d.Reason)
		}
	}
}

// BenchmarkAuthorizeCLI is a call judged by rules on From (a list); with
// "asserted", also by rules on the asserted number.
func BenchmarkAuthorizeCLI(b *testing.B) {
	for _, asserted := range []bool{false, true} {
		name := "from"
		if asserted {
			name = "asserted"
		}
		b.Run(name, func(b *testing.B) {
			s := weighted("priority", []int{1}, nil, nil)
			s.Clients[1] = Client{ID: 1, ResellerID: 7, Active: true, Balance: 1_000_000_000, TariffID: 1, RoutingPlanID: 1}
			var r CLIRules
			r.Add(7, 1, NumberFrom, "", CLIRule{Action: "block"})
			r.Add(7, 1, NumberFrom, "90212", CLIRule{Action: "allow"})
			r.Add(7, 0, NumberFrom, "90850", CLIRule{Action: "rewrite", StripDigits: 2, AddPrefix: "0"})
			src := Source{IP: "10.0.0.1", AnyTransport: true, Caller: "902125550000"}
			if asserted {
				r.Add(7, 1, NumberAsserted, "", CLIRule{Action: "anonymous"})
				r.Add(7, 1, NumberAsserted, "90216", CLIRule{Action: "block"})
				src.Asserted = "902125550000"
			}
			s.CLIRules = r
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s.AuthorizeFrom(src, "905321112233")
			}
		})
	}
}

// BenchmarkAuthorizeNumberRules is a call from a trunk with three number
// rules (00…, +…, 0…) where the number takes the last one tried.
func BenchmarkAuthorizeNumberRules(b *testing.B) {
	s := snapshot()
	s.Trunks[0].NumberRules = rules("00", "", "+", "", "0", "90")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Authorize("10.0.0.1", "05321112233")
	}
}

// IPv6 throughout (T-28): a source written the way OpenSIPS writes it (in
// full, in capitals) finds its trunk, the narrower of two IPv6 networks
// wins, a vendor's IPv6 host is a known source in any spelling, and the
// route to it carries the address in brackets (RFC 3261 19.1.1).
func TestIPv6SourcesAndVendors(t *testing.T) {
	s := snapshot()
	s.Trunks = []Trunk{
		{ID: 1, ClientID: 1, Addrs: []string{"fd30::21", "fd30::8:80/121"}},
		{ID: 2, ClientID: 2, Addrs: []string{"fd30::8:0/112"}},
	}
	c := s.Clients[1]
	c.ID = 2
	s.Clients[2] = c
	conn := s.Connections[2]
	conn.Host = "FD30:0::20"
	s.Connections[2] = conn
	for _, tc := range []struct {
		src   string
		trunk int64
	}{
		{"FD30:0:0:0:0:0:0:21", 1},
		{"fd30::8:5", 2},
		{"FD30:0:0:0:0:0:8:FF", 1}, // last of the /121
		{"fd30::8:ffff", 2},        // last of the /112
		{"fd30::9:0", 0},           // past it
		{"fd30::8:7f", 2},          // just before the /121
		{"fd30::9:1", 0},
	} {
		if d := s.Authorize(tc.src, "905321112233"); d.TrunkID != tc.trunk {
			t.Errorf("%s: trunk %d (%s), want %d", tc.src, d.TrunkID, d.Reason, tc.trunk)
		}
	}
	for _, host := range []string{"fd30::20", "FD30:0:0:0:0:0:0:20", "[fd30::20]"} {
		if !s.KnownSource(host) {
			t.Errorf("vendor host %q is not known", host)
		}
	}
	d := s.Authorize("fd30::21", "905321112233")
	if !d.Allowed || len(d.Routes) == 0 || d.Routes[0].ConnectionID != 2 {
		t.Fatalf("decision: %+v", d)
	}
	if want := "sip:005321112233@[FD30:0::20]:5060;transport=udp"; d.Routes[0].URI != want {
		t.Errorf("route URI %q, want %q", d.Routes[0].URI, want)
	}
}
