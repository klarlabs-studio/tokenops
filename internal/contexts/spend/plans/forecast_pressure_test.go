package plans

import (
	"testing"
	"time"
)

// recentBurnFixture is a five-hour window three hours in at 50% used. On
// average it lasts: 50 points in three hours leaves three hours for the
// other 50, and it resets in two. The last hour used 40 points and the last
// quarter hour 10, so at that rate it runs out in 75 minutes, 45 minutes
// before its reset.
func recentBurnFixture() (VendorWindow, BurnVerdict, time.Time) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	w := VendorWindow{Name: "5h", UsedPct: 50, Duration: 5 * time.Hour, ResetsAt: reset}
	w.Pace = w.PaceAt(now)
	qw := QuotaWindow{Label: "5-hour", UsedPct: 50, ResetsAt: reset, Duration: 5 * time.Hour}
	cur := QuotaReading{At: now, Windows: []QuotaWindow{qw}}
	long := QuotaReading{At: now.Add(-time.Hour), Windows: []QuotaWindow{{Label: "5-hour", UsedPct: 10, ResetsAt: reset, Duration: 5 * time.Hour}}}
	short := QuotaReading{At: now.Add(-15 * time.Minute), Windows: []QuotaWindow{{Label: "5-hour", UsedPct: 40, ResetsAt: reset, Duration: 5 * time.Hour}}}
	return w, AssessBurn(cur, &long, &short, now), now
}

// Pressure, the price of quota and the minutes a window runs out early
// read the one forecast glance shows: the last hour's rate where it was
// measured, the average elsewhere. A window whose average lasts but whose
// last hour would exhaust it is pressed.
func TestPressureReadsTheOneForecast(t *testing.T) {
	avg, verdict, now := recentBurnFixture()
	if p := avg.PaceAt(now); p == nil || !p.LastsToReset {
		t.Fatalf("fixture: the average should last, got %+v", p)
	}
	if _, pressed := avg.Pressed(now); pressed {
		t.Fatalf("fixture: the average alone should not press")
	}
	recent := []VendorWindow{avg}
	ApplyBurn(recent, verdict, now)
	w := recent[0]
	if w.Pace == nil || w.Pace.Basis != BasisRecent || w.Pace.LastsToReset {
		t.Fatalf("fixture: the recent rate should run out, got %+v", w.Pace)
	}

	before, pressed := w.Pressed(now)
	if !pressed || before != 45*time.Minute {
		t.Errorf("Pressed = %v, %v; want 45m, true", before, pressed)
	}
	if p := WindowPressure("Claude", recent, now); p == nil || p.RunsOutBeforeReset != 45*time.Minute {
		t.Errorf("WindowPressure = %+v", p)
	}
	if early, known := w.RunsOutEarly(now); !known || early != 45*time.Minute {
		t.Errorf("RunsOutEarly = %v, %v; want 45m, true", early, known)
	}
	if early, known := avg.RunsOutEarly(now); !known || early != 0 {
		t.Errorf("average RunsOutEarly = %v, %v; want 0, true", early, known)
	}

	price := w.Price(now)
	if price.Basis != PriceBasisRecent || price.RunsOutBeforeReset != 45*time.Minute {
		t.Errorf("price %+v", price)
	}
	// 75 minutes of what is left against 120 to the reset: λ = 1 − 75/120.
	if d := price.Lambda - 0.375; d > 1e-9 || d < -1e-9 {
		t.Errorf("λ = %v, want 0.375", price.Lambda)
	}
	if avgPrice := avg.Price(now); avgPrice.Lambda != 0 || avgPrice.Basis != PriceBasisPace {
		t.Errorf("average price %+v", avgPrice)
	}
}

// The other way round: a window ahead on average whose last hour was idle
// lasts on the forecast, so it is neither pressed nor priced.
func TestAnIdleLastHourIsNoPressure(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	w := VendorWindow{Name: "5h", UsedPct: 80, Duration: 5 * time.Hour, ResetsAt: reset}
	w.Pace = w.PaceAt(now)
	if _, pressed := w.Pressed(now); !pressed {
		t.Fatalf("fixture: the average should press")
	}
	qw := []QuotaWindow{{Label: "5-hour", UsedPct: 80, ResetsAt: reset, Duration: 5 * time.Hour}}
	v := AssessBurn(QuotaReading{At: now, Windows: qw}, &QuotaReading{At: now.Add(-time.Hour), Windows: qw},
		&QuotaReading{At: now.Add(-15 * time.Minute), Windows: qw}, now)
	ws := []VendorWindow{w}
	ApplyBurn(ws, v, now)
	if _, pressed := ws[0].Pressed(now); pressed {
		t.Errorf("idle last hour still pressed: %+v", ws[0].Pace)
	}
	if p := ws[0].Price(now); p.Lambda != 0 || p.Basis != PriceBasisRecent {
		t.Errorf("idle last hour priced: %+v", p)
	}
	if early, known := ws[0].RunsOutEarly(now); !known || early != 0 {
		t.Errorf("RunsOutEarly = %v, %v", early, known)
	}
}

// The forecast is held to the same bar the average is: before a tenth of
// the window has gone by, neither presses above the floor.
func TestTheRecentForecastWaitsForATenthOfTheWindow(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	w := VendorWindow{Name: "5h", UsedPct: 20, Duration: 5 * time.Hour, ResetsAt: now.Add(5*time.Hour - 20*time.Minute)}
	w.Pace = &WindowPace{Status: PaceAhead, DeltaPct: 13, RunsOutIn: time.Hour, Basis: BasisRecent}
	if _, pressed := w.Pressed(now); pressed {
		t.Errorf("pressed twenty minutes in")
	}
	if _, known := w.RunsOutEarly(now); known {
		t.Errorf("run-out known twenty minutes in")
	}
	if p := w.Price(now); p.Basis != PriceBasisShareLeft {
		t.Errorf("priced by pace twenty minutes in: %+v", p)
	}
}

// The pressure says which rate it read.
func TestPressureNamesTheLastHoursRate(t *testing.T) {
	w, verdict, now := recentBurnFixture()
	ws := []VendorWindow{w}
	ApplyBurn(ws, verdict, now)
	p := WindowPressure("Claude", ws, now)
	if p == nil || p.Basis != BasisRecent || p.Why() != "Claude 5h runs out ~45m before it resets at the last hour's rate (50% left)" {
		t.Errorf("pressure %+v: %q", p, p.Why())
	}
}
