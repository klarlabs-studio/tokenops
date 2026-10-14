package windowshare

import (
	"strings"
	"testing"
	"time"
)

const fiveHours = 5 * time.Hour

func samples(kind, scope string, shares ...float64) []Sample {
	out := make([]Sample, 0, len(shares))
	for _, s := range shares {
		out = append(out, Sample{Key: Key{Kind: kind, Scope: scope, Provider: "anthropic", Window: fiveHours}, SharePct: s})
	}
	return out
}

func TestEstimateSuppressedBelowMinimumSamples(t *testing.T) {
	hist := samples("edit", "tokenops", 10, 20, 30)
	est := Estimate(hist, Key{Kind: "edit", Scope: "tokenops", Provider: "anthropic", Window: fiveHours}, 5)
	if est.Status != StatusInsufficient {
		t.Fatalf("status = %q, want insufficient_history below the minimum", est.Status)
	}
	if est.Samples != 3 || est.MinSamples != 5 {
		t.Errorf("samples = %d of %d, want 3 of 5", est.Samples, est.MinSamples)
	}
	if est.LowPct != 0 || est.HighPct != 0 || est.MedianPct != 0 {
		t.Errorf("a suppressed estimate still carries a range: %+v", est)
	}
}

func TestEstimateIsTheInterquartileRangeOfTheSameClassAndScope(t *testing.T) {
	hist := append(samples("edit", "tokenops", 10, 15, 20, 25, 30),
		// Other classes, scopes, providers and windows must not leak in.
		samples("deep", "tokenops", 90, 90, 90, 90, 90)...)
	hist = append(hist, samples("edit", "other-repo", 80, 80)...)
	hist = append(hist, Sample{Key: Key{Kind: "edit", Scope: "tokenops", Provider: "openai", Window: fiveHours}, SharePct: 99})
	hist = append(hist, Sample{Key: Key{Kind: "edit", Scope: "tokenops", Provider: "anthropic", Window: 7 * 24 * time.Hour}, SharePct: 99})

	est := Estimate(hist, Key{Kind: "edit", Scope: "tokenops", Provider: "anthropic", Window: fiveHours}, 5)
	if est.Status != StatusEstimated || est.Basis != BasisClassAndScope {
		t.Fatalf("estimate = %+v, want estimated from class and scope", est)
	}
	if est.Samples != 5 || est.LowPct != 15 || est.MedianPct != 20 || est.HighPct != 25 {
		t.Errorf("estimate = %+v, want 15–25 around 20 from 5 samples", est)
	}
}

func TestEstimateFallsBackToTheClassAcrossScopes(t *testing.T) {
	hist := append(samples("edit", "a", 10, 20), samples("edit", "b", 30, 40, 50)...)
	est := Estimate(hist, Key{Kind: "edit", Scope: "new-repo", Provider: "anthropic", Window: fiveHours}, 5)
	if est.Status != StatusEstimated || est.Basis != BasisClass {
		t.Fatalf("estimate = %+v, want a class-wide estimate when the scope has no history", est)
	}
	if est.Samples != 5 || est.LowPct != 20 || est.HighPct != 40 {
		t.Errorf("estimate = %+v, want 20–40", est)
	}
}

func TestAdviseAgainstWhatIsLeft(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	est := EstimateResult{Status: StatusEstimated, LowPct: 15, MedianPct: 20, HighPct: 25}
	for _, tc := range []struct {
		name string
		used float64
		want Action
	}{
		{"plenty left", 50, ActionProceed},
		{"exactly the high end left", 75, ActionProceed},
		{"only the low end fits", 80, ActionLighterTier},
		{"not even the low end fits", 90, ActionDefer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Advise(est, Reading{Known: true, UsedPct: tc.used, At: now, ResetsAt: now.Add(70 * time.Minute)})
			if got.Action != tc.want {
				t.Errorf("action = %q (%s), want %q", got.Action, got.Reason, tc.want)
			}
			if got.Reason == "" {
				t.Error("advice without a reason")
			}
		})
	}
	if got := Advise(EstimateResult{Status: StatusInsufficient}, Reading{Known: true, UsedPct: 99}); got.Action != ActionNone {
		t.Errorf("advised %q with no estimate; a suppressed estimate must not steer", got.Action)
	}
	if got := Advise(est, Reading{}); got.Action != ActionNone {
		t.Errorf("advised %q with no window reading", got.Action)
	}
}

func TestDeferNamesTheReset(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	got := Advise(EstimateResult{Status: StatusEstimated, LowPct: 30, MedianPct: 35, HighPct: 40},
		Reading{Known: true, UsedPct: 90, At: now, ResetsAt: now.Add(70 * time.Minute)})
	if !strings.Contains(got.Reason, "1h10") {
		t.Errorf("defer reason %q does not say when the window resets", got.Reason)
	}
}

func TestSummaryReadsLikeTheIssue(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	got := Summary(EstimateResult{Status: StatusEstimated, LowPct: 15, MedianPct: 20, HighPct: 25},
		Reading{Known: true, UsedPct: 62, At: now, ResetsAt: now.Add(70 * time.Minute)}, fiveHours)
	want := "typically 15–25% of your 5h window; 38% left, resets in 1h10"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	got = Summary(EstimateResult{Status: StatusInsufficient, Samples: 2, MinSamples: 5}, Reading{Known: true, UsedPct: 62, At: now}, fiveHours)
	if !strings.Contains(got, "2 of 5") || !strings.Contains(got, "38% left") {
		t.Errorf("suppressed summary = %q, want the sample count and what is left", got)
	}
}

func TestMeasureReadsTheShareFromTwoReadingsOfOneWindow(t *testing.T) {
	start := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	reset := start.Add(2 * time.Hour)
	for _, tc := range []struct {
		name      string
		from, to  Reading
		measured  bool
		share     float64
		reasonHas string
	}{
		{"same window", Reading{Known: true, UsedPct: 40, At: start, ResetsAt: reset}, Reading{Known: true, UsedPct: 58, At: start.Add(time.Hour), ResetsAt: reset}, true, 18, ""},
		{"window reset between", Reading{Known: true, UsedPct: 40, At: start, ResetsAt: reset}, Reading{Known: true, UsedPct: 10, At: reset.Add(time.Minute)}, false, 0, "reset"},
		{"usage fell", Reading{Known: true, UsedPct: 40, At: start}, Reading{Known: true, UsedPct: 30, At: start.Add(time.Hour)}, false, 0, "fell"},
		{"no start reading", Reading{}, Reading{Known: true, UsedPct: 30, At: start.Add(time.Hour)}, false, 0, "not measured"},
		{"no end reading", Reading{Known: true, UsedPct: 30, At: start}, Reading{}, false, 0, "not measured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Measure(tc.from, tc.to)
			if got.Measured != tc.measured || got.SharePct != tc.share {
				t.Errorf("measure = %+v, want measured=%v share=%v", got, tc.measured, tc.share)
			}
			if !got.Measured && !strings.Contains(got.Reason, tc.reasonHas) {
				t.Errorf("reason %q does not mention %q", got.Reason, tc.reasonHas)
			}
		})
	}
}

func TestScoreTheEstimateAgainstTheActual(t *testing.T) {
	est := EstimateResult{Status: StatusEstimated, LowPct: 15, MedianPct: 20, HighPct: 25}
	for _, tc := range []struct {
		actual float64
		want   string
	}{{18, DirectionWithin}, {30, DirectionOver}, {5, DirectionUnder}} {
		got := Score(est, Actual{Measured: true, SharePct: tc.actual})
		if !got.Scored || got.Direction != tc.want {
			t.Errorf("score(%v) = %+v, want %s", tc.actual, got, tc.want)
		}
		if got.ErrorPct != tc.actual-20 {
			t.Errorf("score(%v).ErrorPct = %v, want actual minus median", tc.actual, got.ErrorPct)
		}
	}
	if got := Score(EstimateResult{Status: StatusInsufficient}, Actual{Measured: true, SharePct: 3}); got.Scored {
		t.Error("scored a suppressed estimate")
	}
	if got := Score(est, Actual{}); got.Scored {
		t.Error("scored an unmeasured actual")
	}
}

func TestWindowLabel(t *testing.T) {
	for d, want := range map[time.Duration]string{fiveHours: "5h", 7 * 24 * time.Hour: "7d", 24 * time.Hour: "1d"} {
		if got := WindowLabel(d); got != want {
			t.Errorf("WindowLabel(%v) = %q, want %q", d, got, want)
		}
	}
}
