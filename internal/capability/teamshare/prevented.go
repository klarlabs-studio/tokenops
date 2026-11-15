package teamshare

import (
	"maps"
	"math"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// PreventedRows is what the mechanisms prevented, per finished UTC week
// and mechanism (schema 5): the weeks every day of which the upload covers
// and that ended at least teamwire.FinishedWeekLag before now, so a week is
// sent only once it is final. Counts and tokens add the days; dollars add
// the days that priced them and are absent when none did. A lockout window
// counted on two days counts once, its count and its blocked minutes both.
// Never per session or repository.
func PreventedRows(ds []avoidable.Day, covered map[string]bool, now time.Time) ([]string, []teamwire.PreventedWeek) {
	byWeek := map[time.Time][]avoidable.Day{}
	for _, d := range ds {
		if covered[d.Start.UTC().Format(teamwire.DayLayout)] {
			w := mondayOf(d.Start)
			byWeek[w] = append(byWeek[w], d)
		}
	}
	var weeks []string
	var rows []teamwire.PreventedWeek
	for _, w := range slices.SortedFunc(maps.Keys(byWeek), time.Time.Compare) {
		days := byWeek[w]
		if len(days) != 7 || !teamwire.FinishedWeek(w, now) {
			continue
		}
		label := w.Format(teamwire.DayLayout)
		weeks = append(weeks, label)
		rows = append(rows, weekRows(label, days)...)
	}
	return weeks, rows
}

// weekRows adds one week's days into a row per mechanism that acted.
func weekRows(week string, days []avoidable.Day) []teamwire.PreventedWeek {
	type sum struct {
		count, tokens int64
		usd           float64
		priced        bool
		// windows is the largest blocked minutes each lockout window
		// reported, so a window on several days counts once.
		windows map[avoidable.AvoidedWindow]float64
		// minutes are the blocked minutes of lockouts that named no window.
		minutes float64
	}
	sums := map[string]*sum{}
	for _, d := range days {
		for _, m := range d.Report.Prevented.Mechanisms {
			if m.Count <= 0 {
				continue
			}
			s := sums[m.Key]
			if s == nil {
				s = &sum{windows: map[avoidable.AvoidedWindow]float64{}}
				sums[m.Key] = s
			}
			s.tokens += m.Tokens
			if v, ok := m.USD.Amount(); ok {
				s.usd, s.priced = s.usd+v, true
			}
			if len(m.Windows) == 0 {
				s.count += int64(m.Count)
				s.minutes += m.BlockedMinutes
				continue
			}
			for _, w := range m.Windows {
				k := avoidable.AvoidedWindow{Provider: w.Provider, Window: w.Window, ResetsAt: w.ResetsAt.UTC()}
				prev, seen := s.windows[k]
				if !seen {
					s.count++
				}
				s.windows[k] = math.Max(prev, w.BlockedMinutes)
			}
		}
	}
	var out []teamwire.PreventedWeek
	for _, key := range teamwire.Mechanisms {
		s := sums[key]
		if s == nil || s.count == 0 {
			continue
		}
		r := teamwire.PreventedWeek{Week: week, Mechanism: key, Count: s.count, Tokens: s.tokens}
		if s.priced {
			usd := s.usd
			r.USD = &usd
		}
		if key == teamwire.MechanismLockout {
			r.BlockedMinutes = blockedMinutes(s.minutes, s.windows)
		}
		out = append(out, r)
	}
	return out
}

// blockedMinutes is a week's blocked minutes: the unnamed lockouts' plus
// each distinct window's, rounded to whole minutes and capped at the bound
// a row may carry. Windows across providers can add past a week, so the cap
// keeps a busy week's upload valid rather than refused.
func blockedMinutes(unnamed float64, windows map[avoidable.AvoidedWindow]float64) int64 {
	total := unnamed
	for _, m := range windows {
		total += m
	}
	return int64(math.Min(math.Round(total), teamwire.MaxBlockedMinutes))
}

// mondayOf is the Monday 00:00 UTC that starts t's week.
func mondayOf(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}
