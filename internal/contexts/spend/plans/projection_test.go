package plans

import (
	"testing"
	"time"
)

func TestLongestProjection(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	reset := now.Add(3 * 24 * time.Hour)
	win := func(name string, used float64, d, left time.Duration) VendorWindow {
		w := VendorWindow{Name: name, UsedPct: used, Duration: d, ResetsAt: now.Add(left)}
		w.Pace = w.PaceAt(now)
		return w
	}
	session := win("5h", 90, 5*time.Hour, time.Hour)

	// The week, not the fuller five-hour window: 40% after four days is
	// 70% by the reset.
	p := LongestProjection([]VendorWindow{session, win("week", 40, week, 3*24*time.Hour)}, now)
	if p == nil || p.Window != "week" || p.UsedPct != 40 || p.AtResetPct == nil || *p.AtResetPct != 70 ||
		p.RunsOut || p.TooEarly || !p.ResetsAt.Equal(reset) || p.Basis != "" {
		t.Fatalf("projection = %+v", p)
	}

	// Of two weekly windows, the one that runs out.
	p = LongestProjection([]VendorWindow{win("week", 40, week, 3*24*time.Hour), win("week (Fable)", 80, week, 3*24*time.Hour)}, now)
	if p == nil || p.Window != "week (Fable)" || !p.RunsOut || *p.AtResetPct != 140 {
		t.Fatalf("two weeks: %+v", p)
	}

	// Too early in the week: the share, no projection.
	p = LongestProjection([]VendorWindow{win("week", 2, week, week-time.Hour)}, now)
	if p == nil || !p.TooEarly || p.AtResetPct != nil || p.RunsOut {
		t.Fatalf("too early: %+v", p)
	}

	// A stale reading or one whose reset has passed is not projected; with
	// nothing else, the five-hour window is the longest left.
	stale := win("week", 40, week, 3*24*time.Hour)
	stale.Stale = true
	lapsed := VendorWindow{Name: "week", UsedPct: 40, Duration: week, Lapsed: true}
	p = LongestProjection([]VendorWindow{stale, lapsed, session}, now)
	if p == nil || p.Window != "5h" {
		t.Fatalf("stale week: %+v", p)
	}
	if LongestProjection([]VendorWindow{stale, lapsed}, now) != nil {
		t.Error("nothing current to project")
	}
	if LongestProjection(nil, now) != nil {
		t.Error("no windows")
	}
}
