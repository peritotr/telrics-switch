// SPDX-License-Identifier: AGPL-3.0-only

package numplan

import "testing"

func TestCountry(t *testing.T) {
	for number, want := range map[string]string{
		"905321112233": "TR", "4915112345678": "DE", "12125550100": "US", "14165550100": "CA",
		"18765550100": "JM", "79161234567": "RU", "77011234567": "KZ", "442071234567": "GB",
		"8613800138000": "CN", "0": "", "": "",
	} {
		if got := Country(number); got != want {
			t.Errorf("%s: got %q, want %q", number, got, want)
		}
	}
}
