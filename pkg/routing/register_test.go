// SPDX-License-Identifier: AGPL-3.0-only

package routing

import "testing"

// The rule about how a trunk's requests must arrive is one for calls and
// registrations.
func TestTrunkAdmits(t *testing.T) {
	for _, tc := range []struct {
		security, cert, transport, name string
		reason                          string
	}{
		{"any", "", "udp", "", ""},
		{"", "", "tcp", "", ""},
		{"tls", "", "udp", "", "tls required"},
		{"tls", "", "tcp", "", "tls required"},
		{"tls", "", "tls", "", ""},
		{"cert", "pbx.example.com", "tls", "", "client certificate required"},
		{"cert", "pbx.example.com", "tls", "other.example.com", "client certificate required"},
		{"cert", "pbx.example.com", "udp", "pbx.example.com", "client certificate required"},
		{"cert", "pbx.example.com", "tls", "pbx.example.com", ""},
	} {
		reason, ok := Trunk{Security: tc.security, CertName: tc.cert}.Admits(tc.transport, tc.name)
		if reason != tc.reason || ok != (tc.reason == "") {
			t.Errorf("%+v: got %q, %v", tc, reason, ok)
		}
	}
}

func TestRegistrantAndTrunk(t *testing.T) {
	s := &Snapshot{Trunks: []Trunk{
		{ID: 1, ClientID: 1, Addrs: []string{"10.0.0.1"}},
		{ID: 2, ClientID: 1, Username: "pbx", HA1: "abc", TechPrefix: "77", MaxContacts: 3},
		{ID: 3, ClientID: 2, Username: "nopass"},
	}}
	// A trunk that signs in may register whatever its tech prefix; one
	// without a password, or known by its address, may not.
	if tr, ok := s.Registrant("pbx"); !ok || tr.ID != 2 || tr.MaxContacts != 3 {
		t.Errorf("pbx: %+v, %v", tr, ok)
	}
	for _, user := range []string{"nopass", "nobody", ""} {
		if tr, ok := s.Registrant(user); ok {
			t.Errorf("%q may register as %+v", user, tr)
		}
	}
	if tr, ok := s.Trunk(2); !ok || tr.Username != "pbx" {
		t.Errorf("trunk 2: %+v, %v", tr, ok)
	}
	if _, ok := s.Trunk(9); ok {
		t.Error("a trunk that is not there was found")
	}
	// A route to a trunk names the trunk and nothing a vendor's route does.
	r := ToTrunk(2, "905551112233")
	if r.TrunkID != 2 || r.ConnectionID != 0 || r.VendorID != 0 || r.URI != "" || r.Number != "905551112233" {
		t.Errorf("route to a trunk: %+v", r)
	}
}
