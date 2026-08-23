package replay

import (
	"math"
	"testing"
)

// On a flat-rate plan OriginalCostUSD is always 0, so the dollar ratio
// reports "0% saved" for a session where the optimizers demonstrably
// removed tokens. The token ratio is the one that carries signal there.
func TestSavingsRatioTokensOnPlanCoveredSession(t *testing.T) {
	r := Result{
		OriginalInputTokens:    800_000,
		OriginalOutputTokens:   200_000,
		EstimatedSavingsTokens: 250_000,
		OriginalCostUSD:        0, // plan-covered: nothing was billed
	}
	if got := r.SavingsRatio(); got != 0 {
		t.Errorf("SavingsRatio = %f, want 0 for an unbilled session", got)
	}
	got := r.SavingsRatioTokens()
	if math.Abs(got-0.25) > 1e-9 {
		t.Errorf("SavingsRatioTokens = %f, want 0.25 (250k of 1M)", got)
	}
}

// No traffic means no ratio, not a divide-by-zero.
func TestSavingsRatioTokensEmptySession(t *testing.T) {
	if got := (Result{}).SavingsRatioTokens(); got != 0 {
		t.Errorf("SavingsRatioTokens = %f, want 0 for an empty session", got)
	}
}

// The dollar ratio keeps working for metered traffic.
func TestSavingsRatioUSDUnchanged(t *testing.T) {
	r := Result{OriginalCostUSD: 10, EstimatedSavingsUSD: 2.5}
	if got := r.SavingsRatio(); math.Abs(got-0.25) > 1e-9 {
		t.Errorf("SavingsRatio = %f, want 0.25", got)
	}
}
