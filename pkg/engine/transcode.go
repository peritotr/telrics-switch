// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"fmt"
	"os"
	"strconv"
)

// DefaultTranscodeMax is how many calls the media relay may translate at
// once when TRANSCODE_MAX does not say. G.711 to G.711 costs the relay
// little; G.729 and Opus many times more. The figure keeps a small single
// server well clear of saturation with the dearer codecs and is meant to be
// set from a measurement on the server itself (docs/09-kurulum.md).
const DefaultTranscodeMax = 200

// TranscodeMaxFromEnv reads TRANSCODE_MAX, the most calls transcoded at
// once, for Config.TranscodeMax: unset gives 0 (the default), "0" gives -1
// (no call is transcoded), any other whole number itself.
func TranscodeMaxFromEnv() (int, error) {
	v := os.Getenv("TRANSCODE_MAX")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 100000 {
		return 0, fmt.Errorf("TRANSCODE_MAX must be a number of calls between 0 and 100000")
	}
	if n == 0 {
		return -1, nil
	}
	return n, nil
}
