// SPDX-License-Identifier: AGPL-3.0-only

package routing

import (
	"reflect"
	"testing"

	"softswitch/core/pkg/rating"
)

// A call taken for a client is decided as one of that client's from a
// trunk with no rules: same routes, price and funds, no trunk named.
func TestAuthorizeForClient(t *testing.T) {
	s := snapshot()
	own := s.Authorize("10.0.0.1", "905321112233")
	d := s.AuthorizeFor(Inbound{ClientID: 1}, Source{IP: "192.0.2.50", AnyTransport: true, AnyCaller: true}, "905321112233")
	if !d.Allowed || !d.Inbound || d.ClientID != 1 || d.TrunkID != 0 || d.Number != "905321112233" {
		t.Fatalf("decision: %+v", d)
	}
	if !reflect.DeepEqual(d.Routes, own.Routes) || d.Sell != own.Sell || d.MaxDuration != own.MaxDuration || d.Funds != own.Funds {
		t.Errorf("differs from the client's own call: %+v / %+v", d, own)
	}
	if own.Inbound {
		t.Error("a trunk's call marked inbound")
	}
}

func TestAuthorizeForOverrides(t *testing.T) {
	s := snapshot()
	route := ToTrunk(7, "4412345")
	rate := rating.Rate{Prefix: "44", PricePerMin: 60_000, InitialSec: 1, IncrementSec: 1}
	// A number outside the client's tariff and plan, with the hook's price
	// and route.
	d := s.AuthorizeFor(Inbound{ClientID: 1, Number: "4412345", Rate: &rate, Routes: []Route{route}}, Source{IP: "192.0.2.50"}, "+4412345")
	if !d.Allowed || d.Number != "4412345" || d.SellPrefix != "44" || d.SellPrice != 60_000 || d.MaxDuration != 1000 ||
		!reflect.DeepEqual(d.Routes, []Route{route}) {
		t.Fatalf("decision: %+v", d)
	}
	// Without them, the same number is the tariff's to refuse.
	if d := s.AuthorizeFor(Inbound{ClientID: 1}, Source{IP: "192.0.2.50"}, "4412345"); d.Allowed || d.SIPCode != 403 || d.ClientID != 1 || !d.Inbound {
		t.Fatalf("outside the tariff: %+v", d)
	}
}

func TestAuthorizeForRefusals(t *testing.T) {
	s := snapshot()
	src := Source{IP: "192.0.2.50"}
	// A client the snapshot does not have: as unknown as before.
	for _, id := range []int64{0, 42} {
		if d := s.AuthorizeFor(Inbound{ClientID: id}, src, "905321112233"); d.Allowed || d.Reason != UnknownSource || d.ClientID != 0 {
			t.Errorf("client %d: %+v", id, d)
		}
	}
	// A suspended client: refused, and named so the refusal is recorded.
	c := s.Clients[1]
	c.Active = false
	s.Clients[1] = c
	if d := s.AuthorizeFor(Inbound{ClientID: 1}, src, "905321112233"); d.Allowed || d.Reason != "account inactive" || d.ClientID != 1 || d.TrunkID != 0 {
		t.Errorf("inactive: %+v", d)
	}
}
