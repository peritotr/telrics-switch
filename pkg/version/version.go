// SPDX-License-Identifier: AGPL-3.0-only

// Package version names the release a binary was built from. The release
// images set it at build time:
//
//	go build -ldflags "-X softswitch/core/pkg/version.Version=v0.1.0" ...
//
// A binary built without it (go run, go test, a plain go build) reports
// "dev", with the commit and "-dirty" when the Go toolchain recorded them.
package version

import "runtime/debug"

// Version is set by the linker for release builds (a tag such as v0.1.0,
// or what `git describe --tags --always --dirty` gave for the checkout).
// Left empty, Get falls back to the build's own information.
var Version = ""

// Get returns the version this binary reports to the API, the panel and
// the metrics.
func Get() string {
	if Version != "" {
		return Version
	}
	return fromBuildInfo(debug.ReadBuildInfo())
}

// fromBuildInfo is split out so the fallback can be tested: the toolchain
// records the commit only when it builds inside a git checkout.
func fromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "dev"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	v := "dev-" + rev
	if dirty {
		v += "-dirty"
	}
	return v
}
