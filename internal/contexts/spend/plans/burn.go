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

// BurnProjection is a window both look-backs measured: when it runs out at
// their rate, the later of the two, or zero when either lasts to the reset.
type BurnProjection struct {
	Window    QuotaWindow
	RunsOutAt time.Time
}

// BurnVerdict is the burn-rate reading of a plan's windows.
type BurnVerdict struct {
	// Warnings are the windows that run out before their reset, the one
	// that runs out first first.
	Warnings []BurnWarning
	// Projections are every window both look-backs measured, the ones
	// that last included: the one forecast every surface shows (ApplyBurn).
	Projections []BurnProjection
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
		if w.UsedPct >= 100 || w.UsedPct <= 0 || w.Duration <= 0 || !w.ResetsAt.After(now) {
			continue
		}
		longAt, longBurn, longOK := projectBurn(w, cur.At, long, BurnLongLookback)
		shortAt, shortBurn, shortOK := projectBurn(w, cur.At, short, BurnShortLookback)
		if !longOK || !shortOK {
			continue
		}
		// Either look-back that lasts to the reset says the window does:
		// the forecast claims no more urgency than both agree on.
		if longAt.IsZero() || shortAt.IsZero() || !longAt.Before(w.ResetsAt) || !shortAt.Before(w.ResetsAt) {
			v.Projections = append(v.Projections, BurnProjection{Window: w})
			continue
		}
		runsOut := longAt
		if shortAt.After(runsOut) {
			runsOut = shortAt
		}
		v.Projections = append(v.Projections, BurnProjection{Window: w, RunsOutAt: runsOut})
		v.Warnings = append(v.Warnings, BurnWarning{Window: w, RunsOutAt: runsOut, LongBurn: longBurn, ShortBurn: shortBurn})
	}
	sort.Slice(v.Warnings, func(i, j int) bool { return v.Warnings[i].RunsOutAt.Before(v.Warnings[j].RunsOutAt) })
	return v
}

// projectBurn is when w runs out at the rate since past, zero when nothing
// was used over the look-back, and that rate as a multiple of the even
// one; ok when the look-back could be measured at all.
func projectBurn(w QuotaWindow, at time.Time, past *QuotaReading, lookback time.Duration) (time.Time, float64, bool) {
	fromPct, fromAt, ok := baseline(w, at, past, lookback)
	if !ok {
		return time.Time{}, 0, false
	}
	span := at.Sub(fromAt)
	if span <= 0 {
		return time.Time{}, 0, false
	}
	used := w.UsedPct - fromPct
	if used <= 0 {
		return time.Time{}, 0, true
	}
	perPct := float64(span) / used
	runsOut := at.Add(time.Duration(perPct * (100 - w.UsedPct)))
	even := 100 / float64(w.Duration)
	burn := (used / float64(span)) / even
	return runsOut, burn, true
}

// ApplyBurn makes each window's pace say what the burn-rate reading says,
// where it measured the window: one forecast, whichever surface shows it.
// The coach warned from the last hour's rate while glance projected the
// window's average, and for the same window one said it lasts and the
// other that it runs out in two days. A window the look-backs did not
// measure keeps its average pace. Windows match by length and reset: the
// two readers name them differently.
func ApplyBurn(ws []VendorWindow, v BurnVerdict, now time.Time) {
	for i := range ws {
		w := &ws[i]
		if w.Pace == nil || w.Pace.Status == PaceUsedUp {
			continue
		}
		for _, p := range v.Projections {
			if p.Window.Duration != w.Duration || !sameReset(p.Window.ResetsAt, w.ResetsAt) {
				continue
			}
			pace := *w.Pace
			pace.Basis, pace.TooEarly = BasisRecent, false
			pace.LastsToReset, pace.RunsOutIn = p.RunsOutAt.IsZero(), 0
			if !pace.LastsToReset {
				pace.RunsOutIn = max(0, p.RunsOutAt.Sub(now)).Round(time.Minute)
			}
			w.Pace = &pace
			break
		}
	}
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
