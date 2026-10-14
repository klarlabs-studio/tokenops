package plans

import (
	"sort"
	"time"
)

// Burn-rate warnings treat a plan window as an error budget (the SRE
// multi-window alert): a window is about to run out only when the rate of
// the last hour and the rate of the last quarter hour both use up what is
// left before it resets. A long look-back alone warns after a burst that is
// already over; a short one alone warns on every spike. Static shares get
// both ends wrong: 90% used ten minutes before a reset is fine, 50% half an
// hour into a five-hour window is not.
const (
	// BurnLongLookback is the slow look-back: the pace of the last hour.
	BurnLongLookback = time.Hour
	// BurnShortLookback is the fast look-back. Fifteen minutes rather than
	// the textbook ten: the slowest Claude sources record every ten
	// minutes when nothing changes, so ten would often find no reading.
	BurnShortLookback = 15 * time.Minute
	// BurnStaleAfter is the age past which a reading says nothing about
	// the last quarter hour. Vendor meters lag (ADR 0011); a forecast from
	// an old reading is a guess, so none is made.
	BurnStaleAfter = 15 * time.Minute
	// burnResetGranularity absorbs the drift between two readings of the
	// same window's reset time.
	burnResetGranularity = 10 * time.Minute
)

// QuotaReading is every window a plan's meter reported at one moment.
type QuotaReading struct {
	At      time.Time
	Windows []QuotaWindow
}

// BurnWarning is a window that both look-backs run out before its reset.
type BurnWarning struct {
	Window QuotaWindow
	// RunsOutAt is when the window reaches 100%: the later of the two
	// look-backs' projections, so the warning never claims more urgency
	// than both agree on.
	RunsOutAt time.Time
	// LongBurn and ShortBurn are each look-back's rate as a multiple of
	// the even rate that spends the window exactly at its reset.
	LongBurn  float64
	ShortBurn float64
}

// BeforeReset is how long before the reset the window runs out.
func (b BurnWarning) BeforeReset() time.Duration { return b.Window.ResetsAt.Sub(b.RunsOutAt) }

// BurnVerdict is the burn-rate reading of a plan's windows.
type BurnVerdict struct {
	// Warnings are the windows that run out before their reset, the one
	// that runs out first first.
	Warnings []BurnWarning
	// Stale is a reading too old to forecast from: no warning is made.
	Stale bool
}

// AssessBurn reads cur against the readings a long and a short look-back
// ago. Either may be nil when the store holds none; a window younger than
// a look-back is measured from its own start instead, at 0%.
func AssessBurn(cur QuotaReading, long, short *QuotaReading, now time.Time) BurnVerdict {
	if now.Sub(cur.At) > BurnStaleAfter {
		return BurnVerdict{Stale: true}
	}
	var v BurnVerdict
	for _, w := range cur.Windows {
		if w.UsedPct >= 100 || w.UsedPct <= 0 || w.Duration <= 0 || !w.ResetsAt.After(cur.At) {
			continue
		}
		longAt, longBurn, ok := projectBurn(w, cur.At, long, BurnLongLookback)
		if !ok {
			continue
		}
		shortAt, shortBurn, ok := projectBurn(w, cur.At, short, BurnShortLookback)
		if !ok {
			continue
		}
		runsOut := longAt
		if shortAt.After(runsOut) {
			runsOut = shortAt
		}
		v.Warnings = append(v.Warnings, BurnWarning{Window: w, RunsOutAt: runsOut, LongBurn: longBurn, ShortBurn: shortBurn})
	}
	sort.Slice(v.Warnings, func(i, j int) bool { return v.Warnings[i].RunsOutAt.Before(v.Warnings[j].RunsOutAt) })
	return v
}

// projectBurn is when w runs out at the rate since past, and that rate as a
// multiple of the even one; ok only when it runs out before the reset.
func projectBurn(w QuotaWindow, at time.Time, past *QuotaReading, lookback time.Duration) (time.Time, float64, bool) {
	fromPct, fromAt, ok := baseline(w, at, past, lookback)
	if !ok {
		return time.Time{}, 0, false
	}
	span := at.Sub(fromAt)
	used := w.UsedPct - fromPct
	if span <= 0 || used <= 0 {
		return time.Time{}, 0, false
	}
	perPct := float64(span) / used
	runsOut := at.Add(time.Duration(perPct * (100 - w.UsedPct)))
	even := 100 / float64(w.Duration)
	burn := (used / float64(span)) / even
	return runsOut, burn, runsOut.Before(w.ResetsAt)
}

// baseline is the share used at the start of the look-back: the past
// reading of the same window, or the window's own start when it began
// inside the look-back. A past reading of an earlier window (a reset since)
// does not describe this one.
func baseline(w QuotaWindow, at time.Time, past *QuotaReading, lookback time.Duration) (float64, time.Time, bool) {
	start := w.ResetsAt.Add(-w.Duration)
	if past != nil {
		for _, p := range past.Windows {
			if p.Label == w.Label && sameReset(p.ResetsAt, w.ResetsAt) && past.At.Before(at) {
				return p.UsedPct, past.At, true
			}
		}
	}
	if start.After(at.Add(-lookback)) && start.Before(at) {
		return 0, start, true
	}
	return 0, time.Time{}, false
}

func sameReset(a, b time.Time) bool {
	d := a.Sub(b)
	return d < burnResetGranularity && d > -burnResetGranularity
}
