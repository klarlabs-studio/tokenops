package plans

import (
	"math"
	"time"
)

// Pace verdicts.
const (
	PaceOnPace = "on_pace"
	PaceBehind = "behind"
	PaceAhead  = "ahead"
)

// onPaceBand is how far, in points, use may stray from the share of the
// window gone by and still be on pace.
const onPaceBand = 5

// WindowPace says whether a window lasts to its reset at the rate it has
// been used so far.
type WindowPace struct {
	// Status is on_pace, behind (lasts to the reset) or ahead.
	Status string `json:"status"`
	// DeltaPct is the share used minus the share of the window elapsed,
	// in points: +37 is 37 points ahead of an even spread.
	DeltaPct float64 `json:"delta_pct"`
	// LastsToReset is whether, at the rate so far, the window holds
	// until it resets.
	LastsToReset bool `json:"lasts_to_reset"`
	// RunsOutIn is when it runs out at that rate, when it does not last.
	RunsOutIn time.Duration `json:"runs_out_in_ns,omitempty"`
}

// PaceAt is w's pace at now; nil when its length or reset is unknown,
// nothing is used yet, or no time has passed.
func (w VendorWindow) PaceAt(now time.Time) *WindowPace {
	if w.Duration <= 0 || w.ResetsAt.IsZero() || w.UsedPct <= 0 {
		return nil
	}
	left := w.ResetsAt.Sub(now)
	if left <= 0 || left > w.Duration {
		return nil
	}
	elapsed := w.Duration - left
	if elapsed <= 0 {
		return nil
	}
	gone := float64(elapsed) / float64(w.Duration) * 100
	p := &WindowPace{DeltaPct: math.Round(w.UsedPct - gone)}
	switch {
	case math.Abs(w.UsedPct-gone) < onPaceBand:
		p.Status = PaceOnPace
	case w.UsedPct < gone:
		p.Status = PaceBehind
	default:
		p.Status = PaceAhead
	}
	if w.UsedPct >= 100 {
		return p
	}
	// At the rate so far, the rest of the window lasts this long.
	toFull := time.Duration((100 - w.UsedPct) / w.UsedPct * float64(elapsed))
	p.LastsToReset = toFull >= left
	if !p.LastsToReset {
		p.RunsOutIn = toFull.Round(time.Minute)
	}
	return p
}
