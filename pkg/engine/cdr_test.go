// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A leg delivered to a client's trunk names the trunk and no connection; a
// leg to a vendor names the connection and leaves the trunk out of the JSON
// altogether, so that a rater that does not know the field yet still takes
// it during an upgrade.
func TestCDRNamesWhereTheLegWent(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	c := &Call{ClientID: 1, TrunkID: 2, Number: "905321112233", AttemptMs: now.UnixMilli(), Leg: 1,
		Routes: []callRoute{{ConnectionID: 5, URI: "sip:a"}, {TrunkID: 9, URI: "sip:pbx"}}}

	vendor := c.cdr("v", now)
	if vendor.ConnectionID == nil || *vendor.ConnectionID != 5 || vendor.DstTrunkID != nil {
		t.Fatalf("vendor leg: connection %v, trunk %v", vendor.ConnectionID, vendor.DstTrunkID)
	}
	if b, _ := json.Marshal(vendor); strings.Contains(string(b), "dst_trunk_id") {
		t.Fatalf("vendor leg carries the trunk field: %s", b)
	}

	c.Idx, c.Leg = 1, 2
	trunk := c.cdr("v", now)
	if trunk.ConnectionID != nil || trunk.DstTrunkID == nil || *trunk.DstTrunkID != 9 {
		t.Fatalf("trunk leg: connection %v, trunk %v", trunk.ConnectionID, trunk.DstTrunkID)
	}
	b, _ := json.Marshal(trunk)
	if !strings.Contains(string(b), `"dst_trunk_id":9`) || !strings.Contains(string(b), `"connection_id":null`) {
		t.Fatalf("trunk leg JSON: %s", b)
	}
}

// BenchmarkCDR is the record of an ended attempt as the call path builds
// and encodes it.
func BenchmarkCDR(b *testing.B) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	c := &Call{ClientID: 1, TrunkID: 2, Number: "905321112233", Src: "902125550000", SrcIP: "192.0.2.1",
		AttemptMs: now.UnixMilli(), AnswerMs: now.UnixMilli(), Leg: 1, Routes: []callRoute{{ConnectionID: 5, URI: "sip:a"}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rec := c.cdr("bench", now)
		if _, err := json.Marshal(rec); err != nil {
			b.Fatal(err)
		}
	}
}
