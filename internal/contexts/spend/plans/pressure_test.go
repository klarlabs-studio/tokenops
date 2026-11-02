package plans

import (
	"testing"
	"time"
)

// Only the plan-wide windows count, and only while their reading is live:
// a model-scoped week binds one model, a stale window says nothing about
// now, and a window past its reset has started over.
func TestWindowPressureSkips(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	pressed := VendorWindow{Name: "week", UsedPct: 90, Duration: week, ResetsAt: now.Add(3 * 24 * time.Hour)}
	for name, w := range map[string]VendorWindow{
		"model-scoped": {Name: "week (Fable)", UsedPct: 90, Duration: week, ResetsAt: pressed.ResetsAt},
		"stale":        {Name: "week", UsedPct: 90, Duration: week, ResetsAt: pressed.ResetsAt, Stale: true},
		"reset since":  {Name: "week", UsedPct: 90, Duration: week, ResetsAt: now.Add(-time.Minute)},
	} {
		if p := WindowPressure("Claude", []VendorWindow{w}, now); p != nil {
			t.Errorf("%s: pressure %+v", name, p)
		}
	}
	p := WindowPressure("Claude", []VendorWindow{pressed}, now)
	if p == nil || p.Window != "weekly" || p.RunsOutBeforeReset <= 0 {
		t.Fatalf("pressure %+v", p)
	}
}

// A window with no length or reset (the daemon's figures) is judged by the
// floor alone.
func TestPressedWithoutResetUsesTheFloor(t *testing.T) {
	now := time.Now()
	if _, ok := (VendorWindow{UsedPct: 76}).Pressed(now); !ok {
		t.Error("24% left without a reset is not pressed")
	}
	if _, ok := (VendorWindow{UsedPct: 74}).Pressed(now); ok {
		t.Error("26% left without a reset is pressed")
	}
}

// Once the pace can be judged it decides over the floor: a week used
// exactly on pace is not pressed at 20% left, however the arithmetic
// rounds.
func TestTrustedPaceDecidesOverTheFloor(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	for h := 120; h < 168; h++ {
		w := VendorWindow{Name: "week", UsedPct: float64(h) * 100 / 168, Duration: week, ResetsAt: now.Add(week - time.Duration(h)*time.Hour)}
		if _, ok := w.Pressed(now); ok {
			t.Fatalf("on pace at hour %d (%.1f%% used) is pressed", h, w.UsedPct)
		}
	}
}

func TestRunsOutEarly(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	five := func(used float64, resetIn time.Duration) VendorWindow {
		return VendorWindow{Name: "5h", UsedPct: used, Duration: 5 * time.Hour, ResetsAt: now.Add(resetIn)}
	}
	for _, tc := range []struct {
		name   string
		w      VendorWindow
		before time.Duration
		known  bool
	}{
		// 2.5h in, 75% used: the rest lasts 50m, 1h40m before the reset.
		{"ahead of pace", five(75, 150*time.Minute), 100 * time.Minute, true},
		{"lasts to the reset", five(30, 150*time.Minute), 0, true},
		{"used up", five(100, 90*time.Minute), 90 * time.Minute, true},
		// 10m into the window: too early to trust a pace.
		{"too early", five(20, 290*time.Minute), 0, false},
		{"no reset", VendorWindow{Name: "5h", UsedPct: 80, Duration: 5 * time.Hour}, 0, false},
		{"nothing used", five(0, 150*time.Minute), 0, true},
	} {
		before, known := tc.w.RunsOutEarly(now)
		if before != tc.before || known != tc.known {
			t.Errorf("%s: RunsOutEarly = %v, %v; want %v, %v", tc.name, before, known, tc.before, tc.known)
		}
	}
}
