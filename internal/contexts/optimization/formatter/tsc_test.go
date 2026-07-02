package formatter

import (
	"strings"
	"testing"
)

const tscRaw = `src/foo.ts(10,5): error TS2322: Type 'string' is not assignable to type 'number'.
src/foo.ts(11,9): error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.
Files:                         120
Lines:                       45211
Nodes:                      190233
Identifiers:                 65012
Symbols:                     58211
Types:                       12044
Instantiations:              33210
Memory used:                98211K
Assignability cache size:     4021
I/O Read:                     0.01s
Parse time:                   0.42s
Bind time:                    0.19s
Check time:                   0.88s
Emit time:                    0.00s
Total time:                   1.49s
Found 2 errors.
`

func TestTSC_ErrorsSurviveEveryLevel(t *testing.T) {
	tc := NewTSC()
	critical := []string{
		"src/foo.ts(10,5): error TS2322: Type 'string' is not assignable to type 'number'.",
		"src/foo.ts(11,9): error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.",
		"Found 2 errors.",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := tc.Format([]byte(tscRaw), level)
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

func TestTSC_BalancedDropsStats(t *testing.T) {
	tc := NewTSC()
	res, _ := tc.Format([]byte(tscRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"Files:", "Lines:", "Nodes:", "Memory used:", "Check time:", "Total time:"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept stats line %q:\n%s", noise, compact)
		}
	}
	// Diagnostics and summary must remain.
	if !strings.Contains(compact, "error TS2322") {
		t.Errorf("balanced dropped a diagnostic:\n%s", compact)
	}
	if !strings.Contains(compact, "Found 2 errors.") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestTSC_MonotonicReduction(t *testing.T) {
	tc := NewTSC()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := tc.Format([]byte(tscRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestTSC_NonTSCFallsBackToGeneric(t *testing.T) {
	tc := NewTSC()
	raw := "npm warn deprecated foo@1.0.0: use bar\nadded 214 packages in 3s\n"
	res, ok := tc.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestTSC_DiagnosticIsCritical(t *testing.T) {
	tc := NewTSC()
	if !tc.CriticalLine("src/foo.ts(10,5): error TS2322: Type mismatch") {
		t.Error("tsc diagnostic should be critical")
	}
	if !tc.CriticalLine("Found 3 errors.") {
		t.Error("tsc summary should be critical")
	}
	if tc.CriticalLine("Check time:                   0.88s") {
		t.Error("stats line should not be critical")
	}
}
