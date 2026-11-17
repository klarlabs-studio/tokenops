package plans

import (
	"math"
	"testing"
	"time"
)

// Each window kind gets its own rate: the median list value one percent
// of a reset window held. Model-scoped kinds, open windows and windows
// too empty to measure are left out.
func TestValueRatesMeasureEachWindowKind(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-30 * 24 * time.Hour)
	w1, w2 := now.Add(-13*24*time.Hour), now.Add(-6*24*time.Hour)
	f1, f2, f3 := now.Add(-50*time.Hour), now.Add(-30*time.Hour), now.Add(-10*time.Hour)
	ws := []QuotaWindow{
		{Label: "weekly", UsedPct: 40, ResetsAt: w1, Duration: week},
		{Label: "weekly", UsedPct: 50, ResetsAt: w2, Duration: week},
		{Label: "weekly", UsedPct: 80, ResetsAt: now.Add(time.Hour), Duration: week}, // still open
		{Label: "weekly Fable", UsedPct: 60, ResetsAt: w2, Duration: week},           // model-scoped
		{Label: "5-hour", UsedPct: 20, ResetsAt: f1, Duration: 5 * time.Hour},
		{Label: "5-hour", UsedPct: 40, ResetsAt: f2, Duration: 5 * time.Hour},
		{Label: "5-hour", UsedPct: 2, ResetsAt: f3, Duration: 5 * time.Hour}, // too empty
	}
	value := map[time.Time]float64{w1: 400, w2: 600, f1: 10, f2: 40, f3: 1}
	rates := ValueRates("anthropic", ws, since, now, func(_, to time.Time) float64 { return value[to] })
	if len(rates) != 2 {
		t.Fatalf("rates = %+v, want 5-hour and weekly", rates)
	}
	five, weekly := rates[0], rates[1]
	// 10/20 = 0.5 and 40/40 = 1 per percent; weekly 400/40 = 10, 600/50 = 12.
	if five.Window != "5-hour" || five.Duration != 5*time.Hour || five.Windows != 2 || math.Abs(five.USDPerPct-0.75) > 1e-9 {
		t.Errorf("5-hour rate = %+v", five)
	}
	if weekly.Window != "weekly" || weekly.Windows != 2 || math.Abs(weekly.USDPerPct-11) > 1e-9 || weekly.Provider != "anthropic" {
		t.Errorf("weekly rate = %+v", weekly)
	}
}

func TestValueRatesWithoutValueAreAbsent(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ws := []QuotaWindow{{Label: "weekly", UsedPct: 50, ResetsAt: now.Add(-time.Hour), Duration: week}}
	if got := ValueRates("anthropic", ws, now.Add(-30*24*time.Hour), now, func(time.Time, time.Time) float64 { return 0 }); len(got) != 0 {
		t.Errorf("rates = %+v, want none", got)
	}
}
