package formatter

import (
	"strings"
	"testing"
)

// npmInstallRaw is a realistic `npm install` transcript: several per-package
// deprecation warnings, fetch chatter, the change-summary line, a funding
// advisory, and a clean audit result.
const npmInstallRaw = `npm warn deprecated inflight@1.0.6: This module is not supported, and leaks memory. Do not use it.
npm warn deprecated glob@7.2.3: Glob versions prior to v9 are no longer supported.
npm warn deprecated rimraf@3.0.2: Rimraf versions prior to v4 are no longer supported.
npm warn deprecated har-validator@5.1.5: this library is no longer supported
npm warn deprecated uuid@3.4.0: Please upgrade to version 7 or higher.
npm http fetch GET 200 https://registry.npmjs.org/left-pad 142ms
npm http fetch GET 200 https://registry.npmjs.org/glob 88ms
npm timing reifyNode:node_modules/glob Completed in 12ms
reify:glob: timing reifyNode:node_modules/glob Completed in 12ms

added 210 packages, and audited 211 packages in 8s

42 packages are looking for funding
  run ` + "`npm fund`" + ` for details

found 0 vulnerabilities
`

// npmErrorRaw is an install that fails to resolve a package.
const npmErrorRaw = `npm error code E404
npm error 404 Not Found - GET https://registry.npmjs.org/does-not-exist
npm error 404
npm error 404  'does-not-exist@*' is not in this registry.
npm error A complete log of this run can be found in: /Users/x/.npm/_logs/2026-07-02.log
`

func TestNPM_CriticalSurvivesEveryLevel(t *testing.T) {
	n := NewNPM()
	cases := []struct {
		raw      string
		critical []string
	}{
		{
			raw: npmInstallRaw,
			critical: []string{
				"added 210 packages, and audited 211 packages in 8s",
				"found 0 vulnerabilities",
			},
		},
		{
			raw: npmErrorRaw,
			critical: []string{
				"npm error code E404",
				"npm error 404 Not Found - GET https://registry.npmjs.org/does-not-exist",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := n.Format([]byte(tc.raw), level)
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

func TestNPM_BalancedDropsNoise(t *testing.T) {
	n := NewNPM()
	res, _ := n.Format([]byte(npmInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"npm http fetch",
		"npm timing",
		"reify:glob",
		"npm warn deprecated inflight",
		"npm warn deprecated glob",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
}

func TestNPM_AggressiveCollapsesDeprecations(t *testing.T) {
	n := NewNPM()
	res, _ := n.Format([]byte(npmInstallRaw), LossAggressive)
	compact := string(res.Compact)
	if strings.Contains(compact, "npm warn deprecated") {
		t.Errorf("aggressive should collapse deprecation warnings:\n%s", compact)
	}
	if !strings.Contains(compact, "deprecation warnings") {
		t.Errorf("aggressive should emit a deprecation count:\n%s", compact)
	}
	// The change-summary line must still be present.
	if !strings.Contains(compact, "added 210 packages") {
		t.Errorf("aggressive dropped the change-summary line:\n%s", compact)
	}
}

func TestNPM_MonotonicReduction(t *testing.T) {
	n := NewNPM()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := n.Format([]byte(npmInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestNPM_NonNpmFallsBackToGeneric(t *testing.T) {
	n := NewNPM()
	raw := "Cloning into 'repo'...\nremote: Counting objects: 100% (42/42), done.\nUnpacking objects: 100% (42/42), done.\n"
	res, ok := n.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestNPM_ErrorLineIsCritical(t *testing.T) {
	n := NewNPM()
	for _, line := range []string{
		"npm error code E404",
		"npm ERR! network request failed",
		"found 3 vulnerabilities (1 low, 2 high)",
		"added 210 packages in 8s",
		"removed 5 packages in 1s",
		"changed 2 packages in 400ms",
	} {
		if !n.CriticalLine(line) {
			t.Errorf("expected critical: %q", line)
		}
	}
	for _, line := range []string{
		"npm warn deprecated glob@7.2.3: no longer supported",
		"npm http fetch GET 200 https://registry.npmjs.org/glob 88ms",
		"42 packages are looking for funding",
	} {
		if n.CriticalLine(line) {
			t.Errorf("expected non-critical: %q", line)
		}
	}
}
