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
	// PaceUsedUp is a window with nothing left until it resets.
	PaceUsedUp = "used_up"
)

// onPaceBand is how far, in points, use may stray from the share of the
// window gone by and still be on pace.
const onPaceBand = 5

// WindowPace says whether a window lasts to its reset at the rate it has
// been used so far.
type WindowPace struct {
	// Status is on_pace, behind (lasts to the reset), ahead, or used_up.
	Status string `json:"status"`
	// DeltaPct is the share used minus the share of the window elapsed,
	// in points: +37 is 37 points ahead of an even spread.
	DeltaPct float64 `json:"delta_pct"`
	// LastsToReset is whether, at the rate so far, the window holds
	// until it resets.
	LastsToReset bool `json:"lasts_to_reset"`
	// RunsOutIn is when it runs out at that rate, when it does not last.
	RunsOutIn time.Duration `json:"runs_out_in_ns,omitempty"`
	// TooEarly is a window too little of which has gone by to project:
	// one burst early on would read as running out. Neither LastsToReset
	// nor RunsOutIn says anything then.
	TooEarly bool `json:"too_early,omitempty"`
	// Basis is the rate the projection reads: empty for the window's
	// average so far, BasisRecent for the last hour's (ApplyBurn).
	Basis string `json:"basis,omitempty"`
}

// BasisRecent is a projection from the rate of the last hour and the last
// quarter hour, the coach's run-out warning.
const BasisRecent = "recent"

// RateWords names the rate a forecast with basis projects, in the words
// every surface uses for it: "at the last hour's rate" for BasisRecent,
// "at this pace" for the window's average so far.
func RateWords(basis string) string {
	if basis == BasisRecent {
		return "at the last hour's rate"
	}
	return "at this pace"
}

// Forecast is the one forecast of w at now: the pace ApplyBurn set from the
// rate of the last hour and quarter hour where the look-backs measured the
// window, else its average pace so far (PaceAt). Glance shows it, and the
// pressure rule, the price of quota and the boundaries judge by it, so no
// surface calls a window short that another says lasts.
func (w VendorWindow) Forecast(now time.Time) *WindowPace {
	if w.Pace != nil && w.Pace.Basis == BasisRecent {
		return w.Pace
	}
	return w.PaceAt(now)
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
		p.Status = PaceUsedUp
		return p
	}
	// The same bar the pressure rule holds a projection to (2.25 of the
	// 2026-11-10 review): six minutes into a five-hour window, 4% used
	// projected a run-out in 2h24m.
	if float64(elapsed) < pressureMinElapsed*float64(w.Duration) {
		p.TooEarly = true
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
