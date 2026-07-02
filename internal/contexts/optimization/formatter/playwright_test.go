package formatter

import (
	"strings"
	"testing"
)

const playwrightRaw = `Running 24 tests using 4 workers

  ✓  1 [chromium] › login.spec.ts:5:1 › logs in successfully (1.2s)
  ✓  2 [chromium] › cart.spec.ts:9:1 › adds item to cart (0.9s)
  ✘  3 [chromium] › checkout.spec.ts:14:1 › completes checkout (3.1s)

  1) checkout.spec.ts:14:1 › completes checkout ──────────────────

    Error: expect(received).toBe(expected)

    Expected: "success"
    Received: "error"

  1 failed
    [chromium] › checkout.spec.ts:14:1 › completes checkout
  23 passed (45s)
`

func TestPlaywright_FailureSurvivesEveryLevel(t *testing.T) {
	p := NewPlaywright()
	critical := []string{
		"✘  3 [chromium] › checkout.spec.ts:14:1 › completes checkout (3.1s)",
		"Error: expect(received).toBe(expected)",
		"1 failed",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := p.Format([]byte(playwrightRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range critical {
			if !strings.Contains(compact, strings.TrimSpace(c)) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
}

func TestPlaywright_BalancedDropsPasses(t *testing.T) {
	p := NewPlaywright()
	res, _ := p.Format([]byte(playwrightRaw), LossBalanced)
	compact := string(res.Compact)
	if strings.Contains(compact, "✓") {
		t.Errorf("balanced kept a passing (✓) line:\n%s", compact)
	}
	if strings.Contains(compact, "Running 24 tests") {
		t.Errorf("balanced kept the run preamble:\n%s", compact)
	}
	// The failed test line must still be present.
	if !strings.Contains(compact, "✘  3 [chromium]") {
		t.Errorf("balanced dropped the failed test line:\n%s", compact)
	}
}

func TestPlaywright_MonotonicReduction(t *testing.T) {
	p := NewPlaywright()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := p.Format([]byte(playwrightRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestPlaywright_NonMatchingFallsBackToGeneric(t *testing.T) {
	p := NewPlaywright()
	raw := "npm warn deprecated foo@1.0.0\nadded 12 packages in 2s\n"
	res, ok := p.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
