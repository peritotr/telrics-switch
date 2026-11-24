// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"testing"

	"softswitch/core/pkg/engine"
)

// The engine's answers the API passes on are described by the API's own
// types (engineviews.go); they must say the same as the engine's.
func TestEngineViewsMatch(t *testing.T) {
	for _, pair := range []struct {
		name        string
		api, engine any
	}{
		{"live", liveTraffic{}, engine.LiveView{}},
		{"live call", liveCall{}, engine.LiveCall{}},
		{"ban", firewallBan{}, engine.Ban{}},
		{"probe", probeAnswer{}, struct {
			V int `json:"v"`
			engine.ProbeResult
		}{}},
		{"peer", peerHealth{}, engine.PeerHealth{}},
		{"vendor registration", vendorRegistration{}, engine.VendorRegState{}},
	} {
		a, _ := json.Marshal(SchemaOf(pair.api))
		e, _ := json.Marshal(SchemaOf(pair.engine))
		if string(a) != string(e) {
			t.Errorf("%s: the API describes\n%s\nthe engine writes\n%s", pair.name, a, e)
		}
	}
}
