package formatter

import (
	"slices"
	"strings"
	"testing"
)

// pipInstallRaw is a realistic `pip install` transcript: several per-package
// collection/download lines, already-satisfied requirements, build progress,
// the "Installing collected packages" summary, and the install summary.
const pipInstallRaw = `Collecting requests
  Downloading requests-2.31.0-py3-none-any.whl (62 kB)
Collecting charset-normalizer
  Downloading charset_normalizer-3.3.2.whl (500 kB)
Collecting idna
  Using cached idna-3.6-py3-none-any.whl (61 kB)
Requirement already satisfied: urllib3 in ./venv/lib/python3.11/site-packages (2.2.0)
Requirement already satisfied: certifi in ./venv/lib/python3.11/site-packages (2024.2.2)
  Preparing metadata (setup.py): started
  Building wheel for requests (setup.py): started
  Created wheel for requests: filename=requests-2.31.0.whl
Installing collected packages: idna, charset-normalizer, requests
Successfully installed charset-normalizer-3.3.2 idna-3.6 requests-2.31.0
WARNING: You are using pip version 23.0.1; however, version 24.0 is available.
`

// pipErrorRaw is an install that fails to resolve a package.
const pipErrorRaw = `Collecting foo
ERROR: Could not find a version that satisfies the requirement foo
ERROR: No matching distribution found for foo
`

func TestPip_CriticalSurvivesEveryLevel(t *testing.T) {
	p := NewPip()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: pipInstallRaw,
			critical: []string{
				"Successfully installed charset-normalizer-3.3.2 idna-3.6 requests-2.31.0",
			},
		},
		{
			raw: pipErrorRaw,
			critical: []string{
				"ERROR: Could not find a version that satisfies the requirement foo",
				"ERROR: No matching distribution found for foo",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := p.Format([]byte(tc.raw), level)
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

func TestPip_BalancedDropsNoise(t *testing.T) {
	p := NewPip()
	res, _ := p.Format([]byte(pipInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Collecting requests",
		"Collecting charset-normalizer",
		"Downloading requests",
		"Downloading charset_normalizer",
		"Using cached idna",
		"Requirement already satisfied: urllib3",
		"Requirement already satisfied: certifi",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The install summary must survive.
	if !strings.Contains(compact, "Successfully installed") {
		t.Errorf("balanced dropped the install summary:\n%s", compact)
	}
}

func TestPip_MonotonicReduction(t *testing.T) {
	p := NewPip()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := p.Format([]byte(pipInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestPip_NonPipFallsBackToGeneric(t *testing.T) {
	p := NewPip()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := p.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestPip_AliasesIncludePip3(t *testing.T) {
	aliases := NewPip().Aliases()
	if !slices.Contains(aliases, "pip3") {
		t.Errorf("expected Aliases() to include pip3, got %v", aliases)
	}
}

func TestPip_CriticalLineClassification(t *testing.T) {
	p := NewPip()
	for _, line := range []string{
		"ERROR: Could not find a version that satisfies the requirement foo",
		"ERROR: pip's dependency resolver does not currently take into account all the packages that are installed.",
		"error: subprocess-exited-with-error",
		"Successfully installed requests-2.31.0",
		"Successfully uninstalled requests-2.30.0",
	} {
		if !p.CriticalLine(line) {
			t.Errorf("expected critical: %q", line)
		}
	}
	for _, line := range []string{
		"WARNING: You are using pip version 23.0.1; however, version 24.0 is available.",
		"Collecting requests",
		"Downloading requests-2.31.0-py3-none-any.whl (62 kB)",
		"Requirement already satisfied: urllib3 in ./venv",
	} {
		if p.CriticalLine(line) {
			t.Errorf("expected non-critical: %q", line)
		}
	}
}
