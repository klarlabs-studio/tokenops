package formatter

import (
	"strings"
	"testing"
)

// bundleInstallRaw is a realistic `bundle install` transcript: the metadata
// fetch, many per-gem "Using"/"Fetching"/"Installing" lines, and the
// completion summary.
const bundleInstallRaw = `Fetching gem metadata from https://rubygems.org/........
Using rake 13.1.0
Using concurrent-ruby 1.2.2
Fetching nokogiri 1.16.0
Installing nokogiri 1.16.0 with native extensions
Using rails 7.1.0
Bundle complete! 84 Gemfile dependencies, 210 gems now installed.
Use ` + "`bundle info [gemname]`" + ` to see where a bundled gem is installed.
`

// bundleErrorRaw is an install that cannot resolve a gem.
const bundleErrorRaw = `Fetching gem metadata from https://rubygems.org/....
Could not find gem 'does-not-exist' in any of the gem sources listed in your Gemfile.
`

func TestBundle_CriticalSurvivesEveryLevel(t *testing.T) {
	b := NewBundle()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: bundleInstallRaw,
			critical: []string{
				"Bundle complete! 84 Gemfile dependencies, 210 gems now installed.",
			},
		},
		{
			raw: bundleErrorRaw,
			critical: []string{
				"Could not find gem 'does-not-exist' in any of the gem sources listed in your Gemfile.",
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

func TestBundle_BalancedDropsNoise(t *testing.T) {
	b := NewBundle()
	res, _ := b.Format([]byte(bundleInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Using rake 13.1.0",
		"Using rails 7.1.0",
		"Fetching nokogiri 1.16.0",
		"Installing nokogiri 1.16.0 with native extensions",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The completion summary must survive.
	if !strings.Contains(compact, "Bundle complete!") {
		t.Errorf("balanced dropped completion summary:\n%s", compact)
	}
}

func TestBundle_MonotonicReduction(t *testing.T) {
	b := NewBundle()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := b.Format([]byte(bundleInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestBundle_NonMatchingFallsBackToGeneric(t *testing.T) {
	b := NewBundle()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := b.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
