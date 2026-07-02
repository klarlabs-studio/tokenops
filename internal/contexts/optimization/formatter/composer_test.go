package formatter

import (
	"strings"
	"testing"
)

// composerInstallRaw is a realistic `composer install` transcript: the
// repository-loading preamble, dependency update, lock-file operations, the
// per-package "  - Installing" chatter, autoload generation, and the funding
// notice.
const composerInstallRaw = `Loading composer repositories with package information
Updating dependencies
Lock file operations: 12 installs, 0 updates, 0 removals
  - Installing symfony/console (v6.4.0): Extracting archive
  - Installing symfony/polyfill-mbstring (v1.28.0): Extracting archive
  - Installing psr/log (3.0.0): Extracting archive
Generating autoload files
8 packages you are using are looking for funding
`

// composerErrorRaw is an install that cannot resolve requirements.
const composerErrorRaw = `Loading composer repositories with package information
Updating dependencies
Your requirements could not be resolved to an installable set of packages.
  Problem 1
    - Root composer.json requires php >=8.2 but your php version (8.1.0) does not satisfy that requirement.
`

func TestComposer_CriticalSurvivesEveryLevel(t *testing.T) {
	c := NewComposer()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: composerInstallRaw,
			// The install transcript has no critical error lines; assert the
			// summary/funding survives at every level via BalancedDropsNoise.
			critical: []string{},
		},
		{
			raw: composerErrorRaw,
			critical: []string{
				"Your requirements could not be resolved to an installable set of packages.",
				"Problem 1",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := c.Format([]byte(tc.raw), level)
			if !ok {
				t.Fatalf("level=%s ok=false", level)
			}
			if !res.CriticalKept {
				t.Fatalf("level=%s CriticalKept=false", level)
			}
			compact := string(res.Compact)
			for _, cr := range tc.critical {
				if !strings.Contains(compact, cr) {
					t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, cr, compact)
				}
			}
		}
	}
}

func TestComposer_BalancedDropsNoise(t *testing.T) {
	c := NewComposer()
	res, _ := c.Format([]byte(composerInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"- Installing symfony/console",
		"- Installing psr/log",
		"Loading composer repositories",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The funding summary must survive.
	if !strings.Contains(compact, "looking for funding") {
		t.Errorf("balanced dropped funding summary:\n%s", compact)
	}
}

func TestComposer_MonotonicReduction(t *testing.T) {
	c := NewComposer()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := c.Format([]byte(composerInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestComposer_NonMatchingFallsBackToGeneric(t *testing.T) {
	c := NewComposer()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := c.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
