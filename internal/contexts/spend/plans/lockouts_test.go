package plans

import (
	"testing"
	"time"
)

func TestLockoutsByDay(t *testing.T) {
	base := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	fiveHour := func(at time.Time, used float64, reset time.Time) QuotaReading {
		return QuotaReading{At: at, Windows: []QuotaWindow{{Label: "5-hour", UsedPct: used, ResetsAt: reset, Duration: 5 * time.Hour}}}
	}
	// A window that ran out at 22:00 and reset at 02:00 the next day; one
	// that peaked at 80% and never ran out; a later one that ran out at
	// 10:00 and reset at 11:30.
	r1 := base.Add(26 * time.Hour)
	r2 := base.Add(31 * time.Hour)
	r3 := base.Add(35*time.Hour + 30*time.Minute)
	rs := []QuotaReading{
		fiveHour(base.Add(21*time.Hour), 60, r1),
		fiveHour(base.Add(22*time.Hour), 100, r1),
		fiveHour(base.Add(29*time.Hour), 80, r2),
		fiveHour(base.Add(33*time.Hour), 50, r3),
		fiveHour(base.Add(34*time.Hour), 100, r3),
	}
	got := LockoutsByDay(rs, base.Add(48*time.Hour))
	want := map[string]DayLockouts{
		"2026-10-06": {Lockouts: 1, BlockedMinutes: 120},
		"2026-10-07": {Lockouts: 1, BlockedMinutes: 120 + 90},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for day, w := range want {
		if got[day] != w {
			t.Errorf("%s = %+v, want %+v", day, got[day], w)
		}
	}
	// A window still blocked at until is counted up to until.
	if got := LockoutsByDay(rs[:2], base.Add(23*time.Hour)); got["2026-10-06"].BlockedMinutes != 60 {
		t.Errorf("open window = %+v", got)
	}
}

// A window reset early, with a reset credit, ends its lockout when the
// next window starts, not at the reset the exhausted window named: Codex
// counted whole days blocked while the operator worked on a fresh window.
func TestLockoutsByDayEndAtAnEarlyReset(t *testing.T) {
	base := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	weekly := func(at time.Time, used float64, reset time.Time) QuotaReading {
		return QuotaReading{At: at, Windows: []QuotaWindow{{Label: "weekly", UsedPct: used, ResetsAt: reset, Duration: 7 * 24 * time.Hour}}}
	}
	oldReset := base.Add(5*24*time.Hour + 17*time.Hour) // Oct 30 17:00
	newReset := base.Add(7*24*time.Hour + 7*time.Hour)  // Nov 1 07:00
	rs := []QuotaReading{
		weekly(base.Add(5*time.Hour), 90, oldReset),
		weekly(base.Add(6*time.Hour), 100, oldReset),
		// A credit: a fresh window from 07:00, used on the days after.
		weekly(base.Add(7*time.Hour), 0, newReset),
		weekly(base.Add(2*24*time.Hour), 40, newReset),
	}
	got := LockoutsByDay(rs, base.Add(4*24*time.Hour))
	if d := got["2026-10-25"]; d.Lockouts != 1 || d.BlockedMinutes != 60 {
		t.Errorf("day of the lockout = %+v, want 1 lockout and the hour until the credit", d)
	}
	for _, day := range []string{"2026-10-26", "2026-10-27", "2026-10-28"} {
		if d := got[day]; d.BlockedMinutes != 0 {
			t.Errorf("%s = %+v, want nothing blocked on a fresh window", day, d)
		}
	}
}
