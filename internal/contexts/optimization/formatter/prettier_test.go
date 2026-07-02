package formatter

import (
	"strings"
	"testing"
)

const prettierRaw = `Checking formatting...
[warn] src/app.ts
[warn] src/util/format.ts
[warn] test/app.spec.ts
[warn] Code style issues found in 3 files. Run Prettier with --write to fix.
`

func TestPrettier_FindingsSurviveEveryLevel(t *testing.T) {
	p := NewPrettier()
	critical := []string{
		"[warn] src/app.ts",
		"[warn] src/util/format.ts",
		"[warn] test/app.spec.ts",
		"[warn] Code style issues found in 3 files. Run Prettier with --write to fix.",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := p.Format([]byte(prettierRaw), level)
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

func TestPrettier_BalancedDropsNoise(t *testing.T) {
	p := NewPrettier()
	res, _ := p.Format([]byte(prettierRaw), LossBalanced)
	compact := string(res.Compact)
	// The progress line must be gone.
	if strings.Contains(compact, "Checking formatting") {
		t.Errorf("balanced kept the progress line:\n%s", compact)
	}
	// Per-file warnings and the summary survive.
	if !strings.Contains(compact, "[warn] src/app.ts") {
		t.Errorf("balanced dropped a per-file warning:\n%s", compact)
	}
	if !strings.Contains(compact, "Code style issues found in 3 files") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestPrettier_MonotonicReduction(t *testing.T) {
	p := NewPrettier()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := p.Format([]byte(prettierRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestPrettier_NonMatchingFallsBackToGeneric(t *testing.T) {
	p := NewPrettier()
	raw := "added 214 packages in 3s\nfound 0 vulnerabilities\n"
	res, ok := p.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestPrettier_WarnFileVsSummaryCritical(t *testing.T) {
	p := NewPrettier()
	if !p.CriticalLine("[warn] src/app.ts") {
		t.Error("per-file warn line should be critical")
	}
	if !p.CriticalLine("[error] src/broken.ts: SyntaxError") {
		t.Error("error line should be critical")
	}
	if !p.CriticalLine("[warn] Code style issues found in 3 files. Run Prettier with --write to fix.") {
		t.Error("summary line should be critical")
	}
	if !p.CriticalLine("All matched files use Prettier code style!") {
		t.Error("all-clean summary line should be critical")
	}
}
