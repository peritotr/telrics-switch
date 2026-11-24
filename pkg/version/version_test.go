// SPDX-License-Identifier: AGPL-3.0-only
package version

import (
	"runtime/debug"
	"testing"
)

func TestGet(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "v1.2.3"
	if got := Get(); got != "v1.2.3" {
		t.Errorf("Get() with a linker-set version = %q", got)
	}
}

func TestFromBuildInfo(t *testing.T) {
	info := func(kv ...string) *debug.BuildInfo {
		b := &debug.BuildInfo{}
		for i := 0; i < len(kv); i += 2 {
			b.Settings = append(b.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return b
	}
	for _, c := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build information", nil, false, "dev"},
		{"built outside a checkout", info(), true, "dev"},
		{"clean checkout", info("vcs.revision", "0123456789abcdef0123", "vcs.modified", "false"), true, "dev-0123456789ab"},
		{"changed checkout", info("vcs.revision", "0123456789abcdef0123", "vcs.modified", "true"), true, "dev-0123456789ab-dirty"},
	} {
		if got := fromBuildInfo(c.info, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
