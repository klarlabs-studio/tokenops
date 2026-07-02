package formatter

import (
	"slices"
	"strings"
	"testing"
)

// yarnInstallRaw is a realistic `yarn install` transcript: the four phase
// markers, several deprecation advisory warnings, the lockfile-saved notice,
// and the "Done in Xs" completion line.
const yarnInstallRaw = `yarn install v1.22.19
[1/4] Resolving packages...
[2/4] Fetching packages...
[3/4] Linking dependencies...
warning left-pad@1.0.0: deprecated
warning har-validator@5.1.5: deprecated
warning uuid@3.4.0: deprecated
[4/4] Building fresh packages...
success Saved lockfile.
Done in 12.34s.
`

// yarnErrorRaw is an install that fails to resolve a package.
const yarnErrorRaw = `yarn install v1.22.19
[1/4] Resolving packages...
error Couldn't find any versions for "foo" that matches "^9.9.9"
info Visit https://yarnpkg.com/en/docs/cli/install for documentation.
`

func TestYarn_CriticalSurvivesEveryLevel(t *testing.T) {
	y := NewYarn()
	critical := `error Couldn't find any versions for "foo" that matches "^9.9.9"`
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := y.Format([]byte(yarnErrorRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		if !strings.Contains(string(res.Compact), critical) {
			t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, critical, res.Compact)
		}
	}
}

func TestYarn_BalancedDropsPhases(t *testing.T) {
	y := NewYarn()
	res, _ := y.Format([]byte(yarnInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"[1/4]",
		"[2/4]",
		"[3/4]",
		"[4/4]",
		"warning left-pad",
		"success Saved lockfile",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	if !strings.Contains(compact, "Done in 12.34s.") {
		t.Errorf("balanced dropped the completion line:\n%s", compact)
	}
}

func TestYarn_MonotonicReduction(t *testing.T) {
	y := NewYarn()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := y.Format([]byte(yarnInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestYarn_NonYarnFallsBackToGeneric(t *testing.T) {
	y := NewYarn()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := y.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestYarn_AliasesIncludePnpm(t *testing.T) {
	if !slices.Contains(NewYarn().Aliases(), "pnpm") {
		t.Errorf("expected Aliases() to contain pnpm, got %v", NewYarn().Aliases())
	}
}
