// SPDX-License-Identifier: AGPL-3.0-only

package trace

import (
	"bytes"
	"regexp"
)

// What stands in a stored message where a secret was.
const masked = "***"

// The answer to a digest challenge: quoted or bare.
var digestResponse = regexp.MustCompile(`(?i)(\bresponse\s*=\s*)("[^"]*"|[^\s,]+)`)

// Mask takes what proves a caller's identity out of a SIP message before
// it is stored: the response of a digest answer, and everything after the
// scheme of any other kind of credentials. Whoever reads a trace sees who
// signed in and against which challenge, but nothing that could be replayed
// or attacked offline. A message without credentials is returned as it is.
func Mask(raw []byte) []byte {
	type edit struct {
		start, end int
		with       []byte
	}
	var edits []edit
	headers(raw, func(name, value []byte, start, end int) {
		if !bytes.EqualFold(name, []byte("Authorization")) && !bytes.EqualFold(name, []byte("Proxy-Authorization")) {
			return
		}
		scheme, rest, _ := bytes.Cut(value, []byte(" "))
		var out []byte
		if bytes.EqualFold(scheme, []byte("Digest")) {
			out = digestResponse.ReplaceAll(rest, []byte(`${1}"`+masked+`"`))
		} else {
			out = []byte(masked)
		}
		line := append(append(append(append([]byte{}, name...), ": "...), scheme...), ' ')
		edits = append(edits, edit{start, end, append(line, out...)})
	})
	if len(edits) == 0 {
		return raw
	}
	out := make([]byte, 0, len(raw))
	pos := 0
	for _, e := range edits {
		out = append(append(out, raw[pos:e.start]...), e.with...)
		pos = e.end
	}
	return append(out, raw[pos:]...)
}
