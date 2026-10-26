package plans

import (
	"slices"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// An early reset is a plan window that reset well before the reset time
// its last reading gave, after it had been used. Vendors reset windows on
// their own side (a global or promotional reset), and nothing the operator
// did explains the drop, so it is worth saying out loud.
const (
	// EarlyResetMinUsed is the share a window must have reached in its last
	// reading. A reset of a window barely used is not news, and below this
	// a drop is as likely a meter's noise as a reset.
	EarlyResetMinUsed = 20.0
	// EarlyResetMaxUsed is the share the first reading after the reset may
	// show. A reset leaves the window near empty; a share still well used
	// is a usage figure falling, not a window starting over.
	EarlyResetMaxUsed = 5.0
	// EarlyResetMargin is how far before its reported reset the window must
	// have reset, measured from the first reading after it. A window that
	// resets on time is seen at or after its reset, so an hour is more than
	// any reading or clock lag: a reset this early is the vendor's doing.
	EarlyResetMargin = time.Hour
	// EarlyResetVisibleFor is how long an early reset stays in the glance,
	// measured from the reading that first showed it.
	EarlyResetVisibleFor = 24 * time.Hour
	// EarlyResetLookback is how far back the readings are read to find the
	// reading before a reset: a window lasts at most its length, so the last
	// reading of the window that reset is within one window length.
	EarlyResetLookback = 8 * 24 * time.Hour
)

// resetMoveTolerance absorbs drift between the two readings' reset times.
const resetMoveTolerance = time.Minute

// EarlyReset is one window's early reset: what the window showed before,
// and what the first reading after it showed.
type EarlyReset struct {
	Provider       string        `json:"provider"`
	Name           string        `json:"name"`
	VendorLabel    string        `json:"vendor_label,omitempty"`
	Duration       time.Duration `json:"duration_ns"`
	PrevUsedPct    float64       `json:"prev_used_pct"`
	PrevResetsAt   time.Time     `json:"prev_resets_at"`
	PrevObservedAt time.Time     `json:"prev_observed_at"`
	UsedPct        float64       `json:"used_pct"`
	ObservedAt     time.Time     `json:"observed_at"`
	ResetsAt       time.Time     `json:"resets_at"`
}

// Earliness is how early the reset came, at least: the window reset after
// the previous reading and no later than the first one after it, so the
// gap between the old reset time and that reading is a floor.
func (e EarlyReset) Earliness() time.Duration { return e.PrevResetsAt.Sub(e.ObservedAt) }

// DetectEarlyReset reports whether cur, the reading after prev, shows an
// early reset of the same window. planChanged says the plan (or the
// account) changed between the two readings, which moves the window's
// figures without the window resetting, so nothing is judged then.
//
// The checks, each of which rules out a reading that is not an early reset:
// the window is the same one, with the same length; prev was well used and
// cur is near empty; the old reset was still more than EarlyResetMargin
// away when cur was read, so no on-time reset explains it; and cur's reset
// is a fresh one, moved later than the old and no longer than a window.
func DetectEarlyReset(prev, cur VendorWindow, planChanged bool) (EarlyReset, bool) {
	switch {
	case planChanged,
		prev.Name != cur.Name || prev.VendorLabel != cur.VendorLabel,
		cur.Duration <= 0 || cur.Duration != prev.Duration,
		prev.ObservedAt.IsZero() || !cur.ObservedAt.After(prev.ObservedAt),
		prev.ResetsAt.IsZero() || cur.ResetsAt.IsZero(),
		prev.UsedPct < EarlyResetMinUsed || cur.UsedPct > EarlyResetMaxUsed,
		prev.ResetsAt.Sub(cur.ObservedAt) <= EarlyResetMargin,
		!cur.ResetsAt.After(prev.ResetsAt.Add(resetMoveTolerance)),
		cur.ResetsAt.Sub(cur.ObservedAt) > cur.Duration+notStartedTolerance:
		return EarlyReset{}, false
	}
	return EarlyReset{
		Name: cur.Name, VendorLabel: cur.VendorLabel, Duration: cur.Duration,
		PrevUsedPct: prev.UsedPct, PrevResetsAt: prev.ResetsAt, PrevObservedAt: prev.ObservedAt,
		UsedPct: cur.UsedPct, ObservedAt: cur.ObservedAt, ResetsAt: cur.ResetsAt,
	}, true
}

// EarlyResetsIn finds the early resets of provider's windows, among the
// readings observed in [since, now], oldest first. planChangedBetween
// reports whether the plan changed in a span (plans.History); a plan_type
// change between two readings counts as one too.
func EarlyResetsIn(envs []*eventschema.Envelope, provider eventschema.Provider, since, now time.Time, planChangedBetween func(from, to time.Time) bool) []EarlyReset {
	parse, sources := windowParser(provider)
	if parse == nil {
		return nil
	}
	series := windowSeries(envs, parse, sources, now.Add(-EarlyResetLookback))
	var out []EarlyReset
	for _, rs := range series {
		for i := 1; i < len(rs); i++ {
			prev, cur := rs[i-1], rs[i]
			if cur.at.Before(since) || cur.at.After(now) {
				continue
			}
			planChanged := planChangedBetween(prev.at, cur.at) || planTypeChanged(prev.plan, cur.plan)
			if er, ok := DetectEarlyReset(prev.window, cur.window, planChanged); ok {
				er.Provider = string(provider)
				out = append(out, er)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ObservedAt.Before(out[j].ObservedAt) })
	return out
}

// seriesPoint is one reading of one window, with the plan the vendor said
// it was on at the time.
type seriesPoint struct {
	at     time.Time
	plan   string
	window VendorWindow
}

// windowSeries groups the readings of each window into time order.
func windowSeries(envs []*eventschema.Envelope, parse func(map[string]string, time.Time) []VendorWindow, sources []string, since time.Time) map[string][]seriesPoint {
	series := map[string][]seriesPoint{}
	for _, e := range envs {
		if e == nil || e.Attributes == nil || e.Timestamp.Before(since) || !slices.Contains(sources, e.Source) {
			continue
		}
		for _, w := range stamp(parse(e.Attributes, e.Timestamp), e, e.Timestamp) {
			key := w.Name + "|" + w.VendorLabel
			series[key] = append(series[key], seriesPoint{at: e.Timestamp, plan: e.Attributes["plan_type"], window: w})
		}
	}
	for _, rs := range series {
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].at.Before(rs[j].at) })
	}
	return series
}

func planTypeChanged(prev, cur string) bool {
	return prev != "" && cur != "" && prev != cur
}
