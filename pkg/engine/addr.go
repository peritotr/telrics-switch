// SPDX-License-Identifier: AGPL-3.0-only
package engine

import (
	"net/netip"
	"strings"
)

// canonIP writes a source address the SIP core sent in one form for every
// SIP server and every place it is used: firewall rules, the addresses a
// trunk was registered from, bans, call records. OpenSIPS writes an IPv6
// address in full and in capitals ("FD30:0:0:0:0:0:0:21"); kept as it came,
// the same address would be a different key wherever it is compared as
// text. An IPv4 address (no colon) is returned as it is, without parsing,
// so the common case costs nothing on the call path. Brackets and a zone
// are dropped; text that is no address is returned unchanged, for the
// caller to refuse.
func canonIP(s string) string {
	if !strings.Contains(s, ":") {
		return s
	}
	a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"))
	if err != nil {
		return s
	}
	return a.Unmap().WithZone("").String()
}
