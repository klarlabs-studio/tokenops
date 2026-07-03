package formatter

import (
	"strings"
	"testing"
)

// gemInstallRaw is a realistic `gem install` transcript: the fetch, native
// extension build, documentation-generation progress, and the install
// summary.
const gemInstallRaw = `Fetching rails-7.1.0.gem
Fetching nokogiri-1.16.0.gem
Building native extensions. This could take a while...
Successfully installed nokogiri-1.16.0
Successfully installed rails-7.1.0
Parsing documentation for nokogiri-1.16.0
Installing ri documentation for nokogiri-1.16.0
Parsing documentation for rails-7.1.0
Installing ri documentation for rails-7.1.0
Done installing documentation for nokogiri, rails after 2 seconds
2 gems installed
`

// gemErrorRaw is an install that cannot resolve a gem.
const gemErrorRaw = `Fetching does-not-exist.gem
ERROR:  Could not find a valid gem 'does-not-exist' (>= 0) in any repository
`

func TestGem_CriticalSurvivesEveryLevel(t *testing.T) {
	g := NewGem()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: gemInstallRaw,
			critical: []string{
				"Successfully installed nokogiri-1.16.0",
				"Successfully installed rails-7.1.0",
				"2 gems installed",
			},
		},
		{
			raw: gemErrorRaw,
			critical: []string{
				"ERROR:  Could not find a valid gem 'does-not-exist' (>= 0) in any repository",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := g.Format([]byte(tc.raw), level)
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

func TestGem_BalancedDropsNoise(t *testing.T) {
	g := NewGem()
	res, _ := g.Format([]byte(gemInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Fetching rails-7.1.0.gem",
		"Fetching nokogiri-1.16.0.gem",
		"Building native extensions. This could take a while...",
		"Parsing documentation for nokogiri-1.16.0",
		"Installing ri documentation for nokogiri-1.16.0",
		"Done installing documentation for nokogiri, rails after 2 seconds",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The install summary must survive.
	if !strings.Contains(compact, "Successfully installed rails-7.1.0") {
		t.Errorf("balanced dropped install summary:\n%s", compact)
	}
	if !strings.Contains(compact, "2 gems installed") {
		t.Errorf("balanced dropped gems-installed tally:\n%s", compact)
	}
}

func TestGem_MonotonicReduction(t *testing.T) {
	g := NewGem()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := g.Format([]byte(gemInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestGem_NonMatchingFallsBackToGeneric(t *testing.T) {
	g := NewGem()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := g.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
