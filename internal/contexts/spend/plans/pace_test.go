package plans

import (
	"math"
	"testing"
	"time"
)

func TestPaceAt(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	// 26h into a week is 15.5% gone.
	in := func(left time.Duration) time.Time { return now.Add(left) }
	tests := []struct {
		name string
		w    VendorWindow
		want *WindowPace
	}{
		{"ahead and runs out", VendorWindow{UsedPct: 52, Duration: week, ResetsAt: in(week - 26*time.Hour)},
			&WindowPace{Status: PaceAhead, DeltaPct: 37, RunsOutIn: 24 * time.Hour}},
		{"behind lasts", VendorWindow{UsedPct: 5, Duration: week, ResetsAt: in(week - 26*time.Hour)},
			&WindowPace{Status: PaceBehind, DeltaPct: -10, LastsToReset: true}},
		{"on pace", VendorWindow{UsedPct: 47, Duration: 10 * time.Hour, ResetsAt: in(5*time.Hour + 6*time.Minute)},
			&WindowPace{Status: PaceOnPace, DeltaPct: -2, LastsToReset: true}},
		{"used up", VendorWindow{UsedPct: 100, Duration: week, ResetsAt: in(week - 26*time.Hour)},
			&WindowPace{Status: PaceUsedUp, DeltaPct: 85}},
		{"nothing used", VendorWindow{UsedPct: 0, Duration: week, ResetsAt: in(time.Hour)}, nil},
		{"no length", VendorWindow{UsedPct: 30, ResetsAt: in(time.Hour)}, nil},
		{"no reset", VendorWindow{UsedPct: 30, Duration: week}, nil},
		{"reset past", VendorWindow{UsedPct: 30, Duration: week, ResetsAt: in(-time.Minute)}, nil},
	}
	for _, tt := range tests {
		got := tt.w.PaceAt(now)
		switch {
		case (got == nil) != (tt.want == nil):
			t.Errorf("%s: pace = %+v, want %+v", tt.name, got, tt.want)
		case got != nil && *got != *tt.want:
			t.Errorf("%s: pace = %+v, want %+v", tt.name, *got, *tt.want)
		}
	}
}

// Every surface names a forecast's rate in the same words.
func TestRateWords(t *testing.T) {
	for _, tc := range []struct {
		basis, want string
	}{
		{"", "at this pace"},
		{BasisRecent, "at the last hour's rate"},
		{"unknown", "at this pace"},
	} {
		if got := RateWords(tc.basis); got != tc.want {
			t.Errorf("RateWords(%q) = %q, want %q", tc.basis, got, tc.want)
		}
	}
}

// The projection at the reset reads the rate the forecast reads and holds
// to its too-early bar: above 100 exactly when the forecast says the window
// does not last, on either basis.
func TestProjectAtResetAgreesWithTheForecast(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	reset := now.Add(3 * 24 * time.Hour) // four days of seven gone
	weekly := func(used float64) VendorWindow {
		w := VendorWindow{Name: "week", UsedPct: used, Duration: week, ResetsAt: reset}
		w.Pace = w.PaceAt(now)
		return w
	}
	// recent is a weekly window at used whose last hour went up by
	// lastHour points, and its last quarter hour by a quarter of that.
	recent := func(used, lastHour float64) VendorWindow {
		ws := []VendorWindow{weekly(used)}
		win := func(u float64) []QuotaWindow {
			return []QuotaWindow{{Label: "weekly", UsedPct: u, Duration: week, ResetsAt: reset}}
		}
		v := AssessBurn(QuotaReading{At: now, Windows: win(used)},
			&QuotaReading{At: now.Add(-time.Hour), Windows: win(used - lastHour)},
			&QuotaReading{At: now.Add(-15 * time.Minute), Windows: win(used - lastHour/4)}, now)
		ApplyBurn(ws, v, now)
		if ws[0].Pace.Basis != BasisRecent {
			t.Fatalf("fixture: %+v", ws[0].Pace)
		}
		return ws[0]
	}
	tests := []struct {
		name string
		w    VendorWindow
		want float64
		ok   bool
	}{
		// 40% after four of seven days: 70% by the reset.
		{"average lasts", weekly(40), 70, true},
		// 80% after four days: 140%.
		{"average runs out", weekly(80), 140, true},
		{"average exactly full", weekly(400.0 / 7), 100, true},
		// 72 hours left at 1 point an hour on top of 40%.
		{"recent lasts", recent(40, 0.5), 76, true},
		{"recent runs out", recent(40, 1), 112, true},
		{"recent idle", recent(40, 0), 40, true},
		{"used up", weekly(100), 175, true},
		{"too early", func() VendorWindow {
			w := VendorWindow{UsedPct: 4, Duration: 5 * time.Hour, ResetsAt: now.Add(5*time.Hour - 6*time.Minute)}
			w.Pace = w.PaceAt(now)
			return w
		}(), 0, false},
		{"nothing used", weekly(0), 0, false},
		{"no reset", VendorWindow{UsedPct: 30, Duration: week}, 0, false},
	}
	for _, tt := range tests {
		got, ok := ProjectAtReset(tt.w, now)
		if ok != tt.ok || math.Abs(got-tt.want) > 0.01 {
			t.Errorf("%s: ProjectAtReset = %.2f %v, want %.2f %v", tt.name, got, ok, tt.want, tt.ok)
			continue
		}
		f := tt.w.Forecast(now)
		if !ok {
			if f != nil && !f.TooEarly {
				t.Errorf("%s: no projection, but the forecast has one: %+v", tt.name, f)
			}
			continue
		}
		if f.LastsToReset != (got <= 100) {
			t.Errorf("%s: projected %.2f%% at the reset, forecast lasts = %v", tt.name, got, f.LastsToReset)
		}
	}
}

// A recent-basis pace with no measured share at the reset, set by hand or
// read back from JSON, has no projection rather than a wrong one.
func TestProjectAtResetNeedsTheRecentShare(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	w := VendorWindow{UsedPct: 40, Duration: 7 * 24 * time.Hour, ResetsAt: now.Add(72 * time.Hour),
		Pace: &WindowPace{Status: PaceBehind, LastsToReset: true, Basis: BasisRecent}}
	if pct, ok := ProjectAtReset(w, now); ok {
		t.Errorf("ProjectAtReset = %.1f, want none", pct)
	}
}
