// SPDX-License-Identifier: AGPL-3.0-only

package routing

import "testing"

func TestFirewall(t *testing.T) {
	fw, bad := NewFirewall(
		[]string{"203.0.113.7", "198.51.100.0/24", "10.1.2.3/8", "2001:db8::/32", "not-an-address"},
		[]string{"192.0.2.10", "198.51.100.200", "999.1.1.1"})
	if len(bad) != 2 {
		t.Errorf("unreadable rules: %v", bad)
	}
	for ip, want := range map[string][2]bool{
		"203.0.113.7":        {true, false},
		"203.0.113.8":        {false, false},
		"198.51.100.1":       {true, false},
		"198.51.100.200":     {true, false}, // denied and allowed: denied
		"10.200.0.1":         {true, false}, // a network written with host bits set
		"192.0.2.10":         {false, true},
		"192.0.2.11":         {false, false},
		"2001:db8::1":        {true, false},
		"2001:db9::1":        {false, false},
		"::ffff:203.0.113.7": {true, false}, // the same address, written the long way
		"":                   {false, false},
		"garbage":            {false, false},
	} {
		if blocked, allowed := fw.Verdict(ip); blocked != want[0] || allowed != want[1] {
			t.Errorf("%q: blocked %v allowed %v, want %v", ip, blocked, allowed, want)
		}
	}
	if blocked, allowed := (Firewall{}).Verdict("203.0.113.7"); blocked || allowed {
		t.Error("no rules must judge nothing")
	}
}

func BenchmarkFirewallVerdict(b *testing.B) {
	var deny []string
	for i := 0; i < 200; i++ {
		deny = append(deny, "198.51."+itoa(i)+".0/24")
	}
	fw, _ := NewFirewall(deny, []string{"192.0.2.0/24"})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fw.Verdict("10.0.0.1")
	}
}
