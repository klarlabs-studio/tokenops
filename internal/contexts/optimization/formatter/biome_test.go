package formatter

import (
	"strings"
	"testing"
)

const biomeRaw = `Checking 120 files...

src/foo.ts:10:5 lint/style/useConst  FIXABLE  ━━━━━━━━━━━━━━━

  ✖ This let declares a variable that is never reassigned.

     8 │ function greet() {
  > 10 │   let name = "world";
        │       ^^^^
    11 │   return name;

  ℹ Consider using const instead.

src/bar.ts:3:1 lint/suspicious/noDebugger  ━━━━━━━━━━━━━━━

  ⚠ Unexpected debugger statement.

     3 │ debugger;
       │ ^^^^^^^^

Checked 120 files in 48ms. Found 1 error.
`

func TestBiome_FindingsSurviveEveryLevel(t *testing.T) {
	b := NewBiome()
	critical := []string{
		"src/foo.ts:10:5 lint/style/useConst  FIXABLE  ━━━━━━━━━━━━━━━",
		"✖ This let declares a variable that is never reassigned.",
		"src/bar.ts:3:1 lint/suspicious/noDebugger  ━━━━━━━━━━━━━━━",
		"Checked 120 files in 48ms. Found 1 error.",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := b.Format([]byte(biomeRaw), level)
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

func TestBiome_BalancedDropsNoise(t *testing.T) {
	b := NewBiome()
	res, _ := b.Format([]byte(biomeRaw), LossBalanced)
	compact := string(res.Compact)
	// Progress line and the code-frame echo (indented source + caret/pointer)
	// must be gone.
	for _, noise := range []string{
		"Checking 120 files...",
		"function greet() {",
		`let name = "world";`,
		"^^^^",
		"return name;",
		"debugger;",
		"^^^^^^^^",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Diagnostic headers, the error marker, and the summary survive.
	if !strings.Contains(compact, "lint/style/useConst") {
		t.Errorf("balanced dropped a diagnostic header:\n%s", compact)
	}
	if !strings.Contains(compact, "✖ This let declares") {
		t.Errorf("balanced dropped the error marker:\n%s", compact)
	}
	if !strings.Contains(compact, "Found 1 error.") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestBiome_MonotonicReduction(t *testing.T) {
	b := NewBiome()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := b.Format([]byte(biomeRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestBiome_NonMatchingFallsBackToGeneric(t *testing.T) {
	b := NewBiome()
	raw := "added 214 packages in 3s\nno vulnerabilities\n"
	res, ok := b.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestBiome_WarningNotCritical(t *testing.T) {
	b := NewBiome()
	if b.CriticalLine("Found 2 warnings.") {
		t.Error("warning summary should not be critical")
	}
	if !b.CriticalLine("src/foo.ts:10:5 lint/style/useConst  FIXABLE") {
		t.Error("diagnostic header should be critical")
	}
	if !b.CriticalLine("  ✖ This let declares a variable that is never reassigned.") {
		t.Error("error marker line should be critical")
	}
	if !b.CriticalLine("Checked 120 files in 48ms. Found 1 error.") {
		t.Error("error summary should be critical")
	}
}
