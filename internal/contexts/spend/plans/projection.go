package plans

import (
	"math"
	"time"
)

// ResetProjection is where a plan's longest window is headed by its reset,
// on the one forecast every surface shows (Forecast).
type ResetProjection struct {
	// Window names the window ("week").
	Window   string    `json:"window"`
	UsedPct  float64   `json:"used_pct"`
	ResetsAt time.Time `json:"resets_at"`
	// AtResetPct is the share it reaches by the reset at the forecast's
	// rate, in whole points (ProjectAtReset); nil when it is too early in
	// the window to tell.
	AtResetPct *float64 `json:"at_reset_pct,omitempty"`
	// RunsOut is true when it runs out before the reset.
	RunsOut  bool `json:"runs_out,omitempty"`
	TooEarly bool `json:"too_early,omitempty"`
	// Basis is the forecast's rate (RateWords).
	Basis string `json:"basis,omitempty"`
}

// LongestProjection is the projection at the reset of the longest of ws
// that has a current forecast: a stale reading, a window whose reset has
// passed and a limit of unknown kind are passed over. Of two windows of
// that length, it is the one projected fullest. nil when none has a
// forecast.
func LongestProjection(ws []VendorWindow, now time.Time) *ResetProjection {
	var best *ResetProjection
	var bestLen time.Duration
	var bestPct float64
	for _, w := range ws {
		if w.Stale || w.Lapsed || w.Name == OtherLimit {
			continue
		}
		f := w.Forecast(now)
		if f == nil {
			continue
		}
		pct, ok := ProjectAtReset(w, now)
		if !ok {
			pct = -1 // too early: below any projection
		}
		if best != nil && (w.Duration < bestLen || (w.Duration == bestLen && pct <= bestPct)) {
			continue
		}
		p := &ResetProjection{Window: w.Name, UsedPct: w.UsedPct, ResetsAt: w.ResetsAt, TooEarly: !ok, Basis: f.Basis}
		if ok {
			p.AtResetPct = ptrFloat(math.Round(pct))
			p.RunsOut = !f.LastsToReset
		}
		best, bestLen, bestPct = p, w.Duration, pct
	}
	return best
}
