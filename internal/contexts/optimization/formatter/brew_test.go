package formatter

import (
	"strings"
	"testing"
)

// brewInstallRaw is a realistic `brew install` transcript: the dependency
// fetch/download banners, the "==> Pouring" bottle-install actions, an
// advisory warning, and the "🍺" success summary.
const brewInstallRaw = `==> Fetching dependencies for foo: libbar, libbaz
==> Fetching libbar
==> Downloading https://ghcr.io/v2/homebrew/core/libbar/blobs/sha256:abc
==> Downloading from https://pkg-containers.githubusercontent.com/blob/def
==> Installing dependencies for foo: libbar, libbaz
==> Pouring libbar--1.0.arm64_sonoma.bottle.tar.gz
==> Fetching foo
==> Downloading https://ghcr.io/v2/homebrew/core/foo/blobs/sha256:def
Warning: Some installed formulae are missing dependencies.
==> Pouring foo--2.0.arm64_sonoma.bottle.tar.gz
🍺  /opt/homebrew/Cellar/foo/2.0: 42 files, 3.2MB
`

// brewErrorRaw is an install that fails to resolve a formula.
const brewErrorRaw = `==> Fetching foo
Error: No available formula with the name "nonexistent"
`

func TestBrew_CriticalSurvivesEveryLevel(t *testing.T) {
	b := NewBrew()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: brewInstallRaw,
			critical: []string{
				"Pouring foo--2.0.arm64_sonoma.bottle.tar.gz",
				"🍺  /opt/homebrew/Cellar/foo/2.0: 42 files, 3.2MB",
			},
		},
		{
			raw: brewErrorRaw,
			critical: []string{
				`Error: No available formula with the name "nonexistent"`,
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := b.Format([]byte(tc.raw), level)
			if !ok {
				t.Fatalf("level=%s ok=false", level)
			}
			if !res.CriticalKept {
				t.Fatalf("level=%s CriticalKept=false", level)
			}
			compact := string(res.Compact)
			for _, c := range tc.critical {
				if !strings.Contains(compact, c) {
					t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
				}
			}
		}
	}
}

func TestBrew_BalancedDropsNoise(t *testing.T) {
	b := NewBrew()
	res, _ := b.Format([]byte(brewInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"==> Downloading",
		"==> Fetching",
		"==> Installing dependencies",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The install action and success summary must survive.
	for _, keep := range []string{
		"==> Pouring foo--2.0",
		"🍺",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped signal %q:\n%s", keep, compact)
		}
	}
}

func TestBrew_AggressiveDropsWarnings(t *testing.T) {
	b := NewBrew()
	res, _ := b.Format([]byte(brewInstallRaw), LossAggressive)
	compact := string(res.Compact)
	if strings.Contains(compact, "Warning:") {
		t.Errorf("aggressive should drop Warning: lines:\n%s", compact)
	}
	// The success summary must still survive.
	if !strings.Contains(compact, "🍺") {
		t.Errorf("aggressive dropped the success summary:\n%s", compact)
	}
}

func TestBrew_MonotonicReduction(t *testing.T) {
	b := NewBrew()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := b.Format([]byte(brewInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestBrew_NonMatchingFallsBackToGeneric(t *testing.T) {
	b := NewBrew()
	raw := "Hello world\nThis is not package output\nJust some plain text\n"
	res, ok := b.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
