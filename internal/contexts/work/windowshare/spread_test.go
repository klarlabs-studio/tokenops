package windowshare

import (
	"strings"
	"testing"
	"time"
)

// Runs of the same task vary by an order of magnitude, so the estimate
// carries the median and the 90th percentile of the same kind's history
// with the sample size, not one middle-half range.
func TestSpreadIsTheMedianAndNinetiethPercentileWithTheSampleSize(t *testing.T) {
	hist := append(samples("edit", "tokenops", 2, 4, 5, 6, 8, 10, 12, 15, 30, 60),
		samples("deep", "tokenops", 90, 90, 90, 90, 90)...)
	key := Key{Kind: "edit", Scope: "tokenops", Provider: "anthropic", Window: fiveHours}
	sp := SpreadOf(hist, key, Estimate(hist, key, 0))
	if sp.Status != SpreadMeasured || sp.Basis != BasisClassAndScope {
		t.Fatalf("spread = %+v, want measured from class and scope", sp)
	}
	if sp.Samples != 10 || sp.P50Pct != 9 || sp.P90Pct != 33 {
		t.Errorf("spread = %+v, want p50 9, p90 33 over 10 samples", sp)
	}
	if sp.Fallback != "" {
		t.Errorf("a measured spread names a fallback: %q", sp.Fallback)
	}
	for _, want := range []string{"10 past tasks", "9%", "33%"} {
		if !strings.Contains(sp.Note, want) {
			t.Errorf("note %q does not say %q", sp.Note, want)
		}
	}
}

// Below five samples the spread says so plainly and names what the
// answer falls back to: the interquartile estimate when a lower minimum
// let one through, else nothing.
func TestSpreadBelowFiveSamplesFallsBackAndSaysSo(t *testing.T) {
	hist := samples("edit", "tokenops", 10, 20, 30)
	key := Key{Kind: "edit", Scope: "tokenops", Provider: "anthropic", Window: fiveHours}

	sp := SpreadOf(hist, key, Estimate(hist, key, 0))
	if sp.Status != SpreadTooFew || sp.Samples != 3 || sp.MinSamples != MinSpreadSamples {
		t.Fatalf("spread = %+v, want too_few with 3 of %d", sp, MinSpreadSamples)
	}
	if sp.P50Pct != 0 || sp.P90Pct != 0 {
		t.Errorf("a too-small sample still carries percentiles: %+v", sp)
	}
	if sp.Fallback != FallbackNone || !strings.Contains(sp.Note, "3 past tasks") || !strings.Contains(sp.Note, "too few") {
		t.Errorf("spread = %+v, want a plain too-few note and no fallback", sp)
	}

	est := Estimate(hist, key, 3)
	sp = SpreadOf(hist, key, est)
	if sp.Status != SpreadTooFew || sp.Fallback != FallbackInterquartile {
		t.Fatalf("spread = %+v, want too_few falling back to the interquartile estimate", sp)
	}
	if !strings.Contains(sp.Note, "interquartile") {
		t.Errorf("note %q does not label the fallback", sp.Note)
	}
}

func TestSummaryWithSpreadAddsThePercentilesAndN(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	est := EstimateResult{Status: StatusEstimated, LowPct: 15, MedianPct: 20, HighPct: 25}
	sp := Spread{Status: SpreadMeasured, Samples: 5, P50Pct: 20, P90Pct: 28}
	got := SummaryWithSpread(est, sp, Reading{Known: true, UsedPct: 62, At: now, ResetsAt: now.Add(70 * time.Minute)}, fiveHours)
	want := "typically 15–25% of your 5h window (p50 20%, p90 28%, n=5); 38% left, resets in 1h10"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got := SummaryWithSpread(est, Spread{Status: SpreadTooFew, Samples: 3}, Reading{}, fiveHours); got != Summary(est, Reading{}, fiveHours) {
		t.Errorf("a too-few spread changed the summary: %q", got)
	}
}
