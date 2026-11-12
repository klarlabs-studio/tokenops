package plans

import (
	"testing"
	"time"
)

// Six minutes into a five-hour window at 4% used projected a run-out in
// 2h24m. Before a tenth of the window has gone by nothing is projected.
func TestPaceIsTooEarlyBeforeATenthOfTheWindow(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	w := VendorWindow{UsedPct: 4, Duration: 5 * time.Hour, ResetsAt: now.Add(5*time.Hour - 6*time.Minute)}
	p := w.PaceAt(now)
	if p == nil || !p.TooEarly || p.RunsOutIn != 0 || p.LastsToReset {
		t.Fatalf("6 minutes in: %+v", p)
	}
	// Half an hour in, at the same rate, it is projected.
	w = VendorWindow{UsedPct: 20, Duration: 5 * time.Hour, ResetsAt: now.Add(4*time.Hour + 30*time.Minute)}
	if p := w.PaceAt(now); p == nil || p.TooEarly || p.LastsToReset || p.RunsOutIn != 2*time.Hour {
		t.Fatalf("30 minutes in: %+v", p)
	}
}

// Every window both look-backs measured has a projection, the ones that
// last included; a window either cannot measure has none.
func TestBurnProjectsEveryMeasuredWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	win := func(label string, used float64, d, resetsIn time.Duration) QuotaWindow {
		return QuotaWindow{Label: label, UsedPct: used, ResetsAt: now.Add(resetsIn), Duration: d}
	}
	week := 7 * 24 * time.Hour
	cur := QuotaReading{At: now, Windows: []QuotaWindow{
		win("5-hour", 60, 5*time.Hour, 2*time.Hour), win("weekly", 40, week, 3*24*time.Hour), win("weekly Fable", 10, week, 3*24*time.Hour),
	}}
	long := &QuotaReading{At: now.Add(-time.Hour), Windows: []QuotaWindow{
		win("5-hour", 30, 5*time.Hour, 2*time.Hour), win("weekly", 39, week, 3*24*time.Hour),
	}}
	short := &QuotaReading{At: now.Add(-15 * time.Minute), Windows: []QuotaWindow{
		win("5-hour", 52, 5*time.Hour, 2*time.Hour), win("weekly", 40, week, 3*24*time.Hour),
	}}
	v := AssessBurn(cur, long, short, now)
	got := map[string]time.Time{}
	for _, p := range v.Projections {
		got[p.Window.Label] = p.RunsOutAt
	}
	if len(got) != 2 || !got["5-hour"].Equal(now.Add(80*time.Minute)) {
		t.Errorf("projections %v; want the 5-hour running out in 80m and the weekly lasting", got)
	}
	if at, ok := got["weekly"]; !ok || !at.IsZero() {
		t.Errorf("weekly: %v, %v; want it measured and lasting (idle in the last quarter hour)", at, ok)
	}
	if len(v.Warnings) != 1 || v.Warnings[0].Window.Label != "5-hour" {
		t.Errorf("warnings %+v", v.Warnings)
	}
}

// Glance's pace says what the coach's warning says wherever the
// look-backs measured the window: one forecast. The readers name windows
// differently, so they match by length and reset.
func TestApplyBurnMakesOneForecast(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	reset := now.Add(3 * 24 * time.Hour)
	weekly := func(used float64) VendorWindow {
		w := VendorWindow{Name: "week", UsedPct: used, Duration: week, ResetsAt: reset}
		w.Pace = w.PaceAt(now)
		return w
	}
	quota := QuotaWindow{Label: "weekly", Duration: week, ResetsAt: reset.Add(3 * time.Minute)}

	// The average lasts (20% used four days in), the last hour does not.
	ws := []VendorWindow{weekly(20)}
	if !ws[0].Pace.LastsToReset {
		t.Fatalf("fixture: %+v", ws[0].Pace)
	}
	ApplyBurn(ws, BurnVerdict{Projections: []BurnProjection{{Window: quota, RunsOutAt: now.Add(48 * time.Hour)}}}, now)
	if p := ws[0].Pace; p.LastsToReset || p.RunsOutIn != 48*time.Hour || p.Basis != BasisRecent || p.Status != PaceBehind {
		t.Errorf("recent burst: %+v", p)
	}

	// The average runs out (90% used), the last hour lasts.
	ws = []VendorWindow{weekly(90)}
	if ws[0].Pace.LastsToReset {
		t.Fatalf("fixture: %+v", ws[0].Pace)
	}
	ApplyBurn(ws, BurnVerdict{Projections: []BurnProjection{{Window: quota}}}, now)
	if p := ws[0].Pace; !p.LastsToReset || p.RunsOutIn != 0 || p.Basis != BasisRecent {
		t.Errorf("quiet hour: %+v", p)
	}

	// A five-hour window that happens to reset with the week is not the
	// week: windows match by length too.
	ws = []VendorWindow{weekly(20)}
	fiveHour := QuotaWindow{Duration: 5 * time.Hour, ResetsAt: reset}
	ApplyBurn(ws, BurnVerdict{Projections: []BurnProjection{{Window: fiveHour, RunsOutAt: now.Add(time.Hour)}}}, now)
	if p := ws[0].Pace; p.Basis != "" {
		t.Errorf("matched a 5-hour projection to the week: %+v", p)
	}

	// A window the look-backs did not measure keeps its average.
	ws = []VendorWindow{weekly(20)}
	other := QuotaWindow{Duration: 5 * time.Hour, ResetsAt: now.Add(time.Hour)}
	ApplyBurn(ws, BurnVerdict{Projections: []BurnProjection{{Window: other, RunsOutAt: now.Add(time.Minute)}}}, now)
	if p := ws[0].Pace; p.Basis != "" || !p.LastsToReset {
		t.Errorf("unmeasured: %+v", p)
	}
}
