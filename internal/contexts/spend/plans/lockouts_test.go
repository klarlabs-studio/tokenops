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
