package routeguard

import (
	"fmt"
	"math"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

// Pressure thresholds. Each errs towards leaving the model alone: a move
// made on a false alarm costs quality the operator did not agree to give
// up, while a missed one costs only what the coach did before.
const (
	// PressureFloorPct is the share left at or below which a window is
	// pressed whatever its pace says, unless the pace (once it can be
	// trusted) says it lasts to the reset. With a quarter of a week left,
	// two long sessions finish it.
	PressureFloorPct = 25.0
	// pressureMinElapsed is the share of a window that must have gone by
	// before its projected run-out counts: half an hour into a 5h window,
	// about 17 hours into a week. Earlier, one busy burst projects a
	// run-out the rest of the window never sees.
	pressureMinElapsed = 0.10
	// pressureMinMargin is how far ahead of its reset a window must be on
	// course to run out, as a share of its length (30 minutes of a 5h
	// window, about 17 hours of a week). Running out shortly before the
	// reset is not worth a worse model for every task until then.
	pressureMinMargin = 0.10
	// pressureLimitsFreshFor matches the boundaries' reading of the same
	// file: Claude Code rewrites it every turn, so an older one belongs to
	// a session that stopped.
	pressureLimitsFreshFor = 30 * time.Minute
	// pressureFiguresFreshFor matches the boundaries' reading of the
	// daemon's figures, which it writes every minute.
	pressureFiguresFreshFor = 5 * time.Minute
)

// Pressure is evidence that the Claude plan is running short: the tightest
// pressed window, what is left of it, and how far ahead of its reset it
// runs out at the pace so far.
type Pressure struct {
	// Vendor names the plan as its users do ("Claude").
	Vendor string `json:"vendor"`
	// Window is "5h" or "weekly".
	Window  string  `json:"window"`
	LeftPct float64 `json:"left_pct"`
	// RunsOutBeforeReset is how long before its reset the window runs
	// out at the pace so far; zero when only the floor pressed it.
	RunsOutBeforeReset time.Duration `json:"runs_out_before_reset_ns,omitempty"`
	// ResetsAt is when the pressed window resets, when known.
	ResetsAt   time.Time `json:"resets_at,omitzero"`
	ObservedAt time.Time `json:"observed_at"`
	FreshUntil time.Time `json:"fresh_until"`
	// Source is where the reading came from: claude_code_statusline or
	// daemon_glance.
	Source string `json:"source"`
}

// Why says, in the operator's words, why the plan counts as pressed.
func (p *Pressure) Why() string {
	if p == nil {
		return ""
	}
	if p.RunsOutBeforeReset > 0 {
		return fmt.Sprintf("%s %s runs out ~%s before it resets at this pace (%s left)",
			p.Vendor, p.Window, span(p.RunsOutBeforeReset), leftPct(p.LeftPct))
	}
	return fmt.Sprintf("%s %s has %s left", p.Vendor, p.Window, leftPct(p.LeftPct))
}

func leftPct(v float64) string { return fmt.Sprintf("%d%%", int(math.Round(max(0, v)))) }

// span renders a countdown compactly: 45m, 3h 10m, 2d 4h.
func span(d time.Duration) string {
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// ReadPressure reads the Claude plan's pressure from what is already on the
// machine: the status line's reading first (it carries resets, so pace),
// the daemon's figures second (share left only). Both are local files; it
// never calls the daemon, so the hook stays fast, and any failure reads as
// no pressure. An empty limitsPath is the status line's default file.
func ReadPressure(limitsPath string, figures boundaryledger.Dir, now time.Time) *Pressure {
	if r, ok, err := claudelimits.Read(limitsPath); err == nil && ok && now.Sub(r.ObservedAt) <= pressureLimitsFreshFor {
		// A fresh status line reading is the vendor's own, so it answers
		// even when it finds no pressure.
		p, _ := PressureFromLimits(r, now)
		return p
	}
	if fig, ok, err := figures.GetFigures(); err == nil && ok {
		p, _ := PressureFromFigures(fig, now)
		return p
	}
	return nil
}

// PressureFromLimits judges the status line's 5h and weekly windows with
// the glance's own pace (plans.VendorWindow.PaceAt) and returns the
// tightest pressed one.
func PressureFromLimits(r claudelimits.Reading, now time.Time) (*Pressure, bool) {
	if r.ObservedAt.IsZero() || now.Sub(r.ObservedAt) > pressureLimitsFreshFor {
		return nil, false
	}
	var best *Pressure
	for _, w := range []struct {
		name string
		win  *claudelimits.Window
		dur  time.Duration
	}{{"5h", r.FiveHour, 5 * time.Hour}, {"weekly", r.SevenDay, 7 * 24 * time.Hour}} {
		if w.win == nil || w.win.ResetsAt <= 0 {
			continue
		}
		vw := plans.VendorWindow{UsedPct: w.win.UsedPct, Duration: w.dur, ResetsAt: time.Unix(w.win.ResetsAt, 0)}
		if !vw.ResetsAt.After(now) {
			continue // reset since: the reading says nothing about it now
		}
		left := max(0, 100-w.win.UsedPct)
		before, pressed := windowPressed(vw, left, now)
		if !pressed || (best != nil && left >= best.LeftPct) {
			continue
		}
		best = &Pressure{
			Vendor: "Claude", Window: w.name, LeftPct: left, RunsOutBeforeReset: before,
			ResetsAt: vw.ResetsAt.UTC(), ObservedAt: r.ObservedAt.UTC(),
			FreshUntil: r.ObservedAt.Add(pressureLimitsFreshFor).UTC(), Source: "claude_code_statusline",
		}
	}
	return best, best != nil
}

// windowPressed applies the rule to one window: on course to run out well
// before its reset once enough of it has gone by, or at the floor unless
// a trustworthy pace says it lasts.
func windowPressed(vw plans.VendorWindow, left float64, now time.Time) (time.Duration, bool) {
	if vw.UsedPct >= 100 {
		return 0, true
	}
	pace := vw.PaceAt(now)
	elapsed := vw.Duration - vw.ResetsAt.Sub(now)
	trusted := pace != nil && float64(elapsed) >= pressureMinElapsed*float64(vw.Duration)
	if trusted && !pace.LastsToReset {
		before := vw.ResetsAt.Sub(now) - pace.RunsOutIn
		if float64(before) >= pressureMinMargin*float64(vw.Duration) {
			return before, true
		}
	}
	if left <= PressureFloorPct && (!trusted || !pace.LastsToReset) {
		return 0, true
	}
	return 0, false
}

// PressureFromFigures judges the daemon's figures for the Claude plan. They
// carry no reset, so only the floor applies.
func PressureFromFigures(fig boundaryledger.Figures, now time.Time) (*Pressure, bool) {
	if fig.ObservedAt.IsZero() || now.Sub(fig.ObservedAt) > pressureFiguresFreshFor {
		return nil, false
	}
	var best *Pressure
	for _, w := range []struct{ key, label string }{{"5h", "5h"}, {"week", "weekly"}} {
		left, ok := fig.PlanLeftPct["anthropic"][w.key]
		if !ok || left > PressureFloorPct || (best != nil && left >= best.LeftPct) {
			continue
		}
		best = &Pressure{
			Vendor: "Claude", Window: w.label, LeftPct: left,
			ObservedAt: fig.ObservedAt.UTC(), FreshUntil: fig.ObservedAt.Add(pressureFiguresFreshFor).UTC(),
			Source: "daemon_glance",
		}
	}
	return best, best != nil
}
