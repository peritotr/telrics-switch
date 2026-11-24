// SPDX-License-Identifier: AGPL-3.0-only

package routing

import "net/netip"

// Firewall holds the operator's rules about source addresses: those to
// drop and those spared the protection against floods. A rule names one
// address or a network.
type Firewall struct {
	deny, allow []netip.Prefix
}

// NewFirewall builds the rules from addresses and networks as text
// ("203.0.113.7", "198.51.100.0/24"); what cannot be read is returned in
// bad and left out.
func NewFirewall(deny, allow []string) (fw Firewall, bad []string) {
	parse := func(list []string) (out []netip.Prefix) {
		for _, text := range list {
			if p, err := netip.ParsePrefix(text); err == nil {
				out = append(out, p.Masked())
			} else if a, err := netip.ParseAddr(text); err == nil {
				out = append(out, netip.PrefixFrom(a, a.BitLen()))
			} else {
				bad = append(bad, text)
			}
		}
		return out
	}
	fw.deny, fw.allow = parse(deny), parse(allow)
	return fw, bad
}

// Verdict says what the rules make of an address: blocked goes before
// allowed, and an address no rule names is neither.
func (f Firewall) Verdict(ip string) (blocked, allowed bool) {
	if len(f.deny) == 0 && len(f.allow) == 0 {
		return false, false
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false, false
	}
	addr = addr.Unmap()
	for _, p := range f.deny {
		if p.Contains(addr) {
			return true, false
		}
	}
	for _, p := range f.allow {
		if p.Contains(addr) {
			return false, true
		}
	}
	return false, false
}
