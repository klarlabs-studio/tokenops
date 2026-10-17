package plans

import (
	"sort"
	"time"
)

// DayLockouts is how often a plan's windows ran out on one UTC day, and
// for how long within the day.
type DayLockouts struct {
	Lockouts       int
	BlockedMinutes int
}

// LockoutsByDay is the plan's actual lockouts in rs, by the UTC day
// (DayLayout "2006-01-02") each window ran out: the replay's measure for
// the plan as it was, with no tier scaling. A window that ran out counts
// once, on the day it ran out; the time blocked, from running out to the
// reset (or until), is split across the days it spans, with overlapping
// windows counted once.
func LockoutsByDay(rs []QuotaReading, until time.Time) map[string]DayLockouts {
	asRead := func(time.Time) (float64, bool) { return 1, true }
	out := map[string]DayLockouts{}
	var spans [][2]time.Time
	for _, w := range instancesOf(rs, asRead) {
		at, ran := w.exhaustedAt(1)
		if !ran {
			continue
		}
		day := at.UTC().Format(time.DateOnly)
		d := out[day]
		d.Lockouts++
		out[day] = d
		end := w.reset
		if end.After(until) {
			end = until
		}
		if end.After(at) {
			spans = append(spans, [2]time.Time{at, end})
		}
	}
	for _, s := range unionSpans(spans) {
		for start := s[0].UTC(); start.Before(s[1]); {
			midnight := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, time.UTC)
			end := midnight
			if s[1].Before(end) {
				end = s[1]
			}
			day := start.Format(time.DateOnly)
			d := out[day]
			d.BlockedMinutes += int(end.Sub(start).Round(time.Minute) / time.Minute)
			out[day] = d
			start = midnight
		}
	}
	return out
}

// unionSpans merges overlapping spans, earliest first.
func unionSpans(spans [][2]time.Time) [][2]time.Time {
	if len(spans) == 0 {
		return nil
	}
	sorted := append([][2]time.Time(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0].Before(sorted[j][0]) })
	out := [][2]time.Time{sorted[0]}
	for _, s := range sorted[1:] {
		last := &out[len(out)-1]
		if !s[0].After(last[1]) {
			if s[1].After(last[1]) {
				last[1] = s[1]
			}
			continue
		}
		out = append(out, s)
	}
	return out
}
