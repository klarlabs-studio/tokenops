package formatter

import (
	"strings"
	"testing"
)

// uvInstallRaw is a realistic `uv pip install` / `uv sync` transcript: the
// resolution summary, per-package progress ("Downloaded"/"Prepared"), the
// install summary, and the per-package "+ pkg==" listing.
const uvInstallRaw = `Resolved 42 packages in 120ms
Downloaded numpy
Downloaded pandas
Downloaded requests
Prepared 42 packages in 60ms
Installed 42 packages in 80ms
 + numpy==1.26.4
 + pandas==2.2.0
 + requests==2.31.0
`

// uvErrorRaw is a sync that fails to resolve.
const uvErrorRaw = `Resolved 0 packages in 40ms
error: No solution found when resolving dependencies:
  Because only numpy==1.0.0 is available and you require numpy>=2.0.0, we can conclude that your requirements are unsatisfiable.
`

func TestUV_CriticalSurvivesEveryLevel(t *testing.T) {
	u := NewUV()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: uvInstallRaw,
			critical: []string{
				"Resolved 42 packages in 120ms",
				"Installed 42 packages in 80ms",
			},
		},
		{
			raw: uvErrorRaw,
			critical: []string{
				"error: No solution found when resolving dependencies:",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := u.Format([]byte(tc.raw), level)
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

func TestUV_BalancedDropsNoise(t *testing.T) {
	u := NewUV()
	res, _ := u.Format([]byte(uvInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Downloaded numpy",
		"Prepared 42 packages",
		"+ numpy==1.26.4",
		"+ pandas==2.2.0",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Summaries must survive.
	for _, keep := range []string{
		"Resolved 42 packages in 120ms",
		"Installed 42 packages in 80ms",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped summary %q:\n%s", keep, compact)
		}
	}
}

func TestUV_MonotonicReduction(t *testing.T) {
	u := NewUV()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := u.Format([]byte(uvInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestUV_NonMatchingFallsBackToGeneric(t *testing.T) {
	u := NewUV()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := u.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
