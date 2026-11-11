package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestStringContainsAllFields(t *testing.T) {
	got := String()
	for _, want := range []string{Version, Commit, Date} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

// A binary built without -ldflags (go install …@latest) reports the module
// version the toolchain recorded, not "dev"; a field the linker set wins.
func TestFromBuildInfo(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v0.118.18"}}
	checkout := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "99d79ea0123456789abcdef"}, {Key: "vcs.time", Value: "2026-11-11T09:00:00Z"},
	}}
	for _, tc := range []struct {
		name                string
		info                *debug.BuildInfo
		v, c, d             string
		wantV, wantC, wantD string
	}{
		{"go install", installed, "dev", "unknown", "unknown", "v0.118.18", "unknown", "unknown"},
		{"checkout", checkout, "dev", "unknown", "unknown", "dev", "99d79ea01234", "2026-11-11T09:00:00Z"},
		{"release", installed, "0.118.18", "abc", "2026-11-10", "0.118.18", "abc", "2026-11-10"},
	} {
		v, c, d := fromBuildInfo(tc.info, tc.v, tc.c, tc.d)
		if v != tc.wantV || c != tc.wantC || d != tc.wantD {
			t.Errorf("%s: %s %s %s; want %s %s %s", tc.name, v, c, d, tc.wantV, tc.wantC, tc.wantD)
		}
	}
}
