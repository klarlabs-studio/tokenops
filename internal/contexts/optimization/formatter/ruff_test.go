package formatter

import (
	"strings"
	"testing"
)

const ruffRaw = `src/app.py:10:5: E501 Line too long (90 > 88)
src/app.py:12:1: F401 ` + "`os`" + ` imported but unused
    import os
    ^^^^^^^^^
src/util.py:3:7: E711 Comparison to None should be 'if cond is None:'
    if x == None:
          ^
Found 3 errors.
[*] 1 fixable with the ` + "`--fix`" + ` option.
`

func TestRuff_FindingsSurviveEveryLevel(t *testing.T) {
	rf := NewRuff()
	critical := []string{
		"src/app.py:10:5: E501 Line too long (90 > 88)",
		"src/app.py:12:1: F401 `os` imported but unused",
		"src/util.py:3:7: E711 Comparison to None should be 'if cond is None:'",
		"Found 3 errors.",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := rf.Format([]byte(ruffRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range critical {
			if !strings.Contains(compact, c) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
}

func TestRuff_BalancedDropsNoise(t *testing.T) {
	rf := NewRuff()
	res, _ := rf.Format([]byte(ruffRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"import os", "if x == None:", "^", "[*]", "fixable"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Findings and summary must remain.
	if !strings.Contains(compact, "E501 Line too long") {
		t.Errorf("balanced dropped a finding:\n%s", compact)
	}
	if !strings.Contains(compact, "Found 3 errors.") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestRuff_MonotonicReduction(t *testing.T) {
	rf := NewRuff()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := rf.Format([]byte(ruffRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestRuff_NonMatchingFallsBackToGeneric(t *testing.T) {
	rf := NewRuff()
	raw := "npm warn deprecated foo@1.0.0: use bar\nadded 214 packages in 3s\n"
	res, ok := rf.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestRuff_FindingIsCritical(t *testing.T) {
	rf := NewRuff()
	if !rf.CriticalLine("src/app.py:10:5: E501 Line too long (90 > 88)") {
		t.Error("ruff finding should be critical")
	}
	if !rf.CriticalLine("Found 3 errors.") {
		t.Error("ruff summary should be critical")
	}
	if rf.CriticalLine("[*] 1 fixable with the `--fix` option.") {
		t.Error("fixable advisory should not be critical")
	}
	if rf.CriticalLine("    ^^^^^^^^^") {
		t.Error("caret line should not be critical")
	}
}
