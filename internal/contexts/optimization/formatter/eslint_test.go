package formatter

import (
	"strings"
	"testing"
)

const eslintRaw = `/app/src/index.ts
  1:1   error    Missing semicolon                 semi
  2:10  warning  Unexpected console statement      no-console
  3:5   error    'x' is assigned but never used    no-unused-vars

/app/src/util.ts
  7:1   warning  Missing return type on function   @typescript-eslint/explicit-function-return-type

✖ 4 problems (2 errors, 2 warnings)
`

func TestESLint_ErrorsSurviveEveryLevel(t *testing.T) {
	e := NewESLint()
	critical := []string{
		"1:1   error    Missing semicolon                 semi",
		"3:5   error    'x' is assigned but never used    no-unused-vars",
		"✖ 4 problems (2 errors, 2 warnings)",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := e.Format([]byte(eslintRaw), level)
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

func TestESLint_BalancedDropsWarnings(t *testing.T) {
	e := NewESLint()
	res, _ := e.Format([]byte(eslintRaw), LossBalanced)
	compact := string(res.Compact)
	// Warning problem lines are advisory and must be gone. (The summary line
	// still legitimately reports the "2 warnings" count.)
	for _, w := range []string{
		"Unexpected console statement",
		"no-console",
		"Missing return type on function",
		"@typescript-eslint/explicit-function-return-type",
	} {
		if strings.Contains(compact, w) {
			t.Errorf("balanced kept warning content %q:\n%s", w, compact)
		}
	}
	// The util.ts header held only a warning, so it must be dropped too.
	if strings.Contains(compact, "/app/src/util.ts") {
		t.Errorf("balanced kept an empty file header:\n%s", compact)
	}
	// The index.ts header (has errors) and its errors survive.
	if !strings.Contains(compact, "/app/src/index.ts") {
		t.Errorf("balanced dropped a header that still has errors:\n%s", compact)
	}
	if !strings.Contains(compact, "Missing semicolon") {
		t.Errorf("balanced dropped an error:\n%s", compact)
	}
	if !strings.Contains(compact, "✖ 4 problems") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestESLint_MonotonicReduction(t *testing.T) {
	e := NewESLint()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := e.Format([]byte(eslintRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestESLint_NonESLintFallsBackToGeneric(t *testing.T) {
	e := NewESLint()
	raw := "added 214 packages in 3s\nfound 0 vulnerabilities\n"
	res, ok := e.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestESLint_WarningNotCritical(t *testing.T) {
	e := NewESLint()
	if e.CriticalLine("  2:10  warning  Unexpected console statement  no-console") {
		t.Error("warning line should not be critical")
	}
	if !e.CriticalLine("  1:1   error    Missing semicolon  semi") {
		t.Error("error line should be critical")
	}
	if !e.CriticalLine("✖ 4 problems (2 errors, 2 warnings)") {
		t.Error("summary line should be critical")
	}
}
