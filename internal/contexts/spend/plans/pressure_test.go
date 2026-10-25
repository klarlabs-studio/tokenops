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
