package plans

import (
	"fmt"
	"math"
	"time"
)

// Pressure thresholds: when a plan window counts as running short enough
// that work which fits a cheaper model should move to one. Each errs
// towards leaving the model alone: a move made on a false alarm costs
// quality the operator did not agree to give up, while a missed one costs
// only what happened before.
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
)

// Pressed reports whether w is running short: on course, at the pace so
// far (PaceAt, the glance's own pace), to run out well before its reset
// once enough of it has gone by to trust that; or at PressureFloorPct or
// less unless a trusted pace says it lasts. before is how long ahead of
// the reset it runs out, zero when only the floor or a used-up window
// pressed it. A window without a length or reset is judged by the floor
// alone.
func (w VendorWindow) Pressed(now time.Time) (before time.Duration, pressed bool) {
	if w.UsedPct >= 100 {
		return 0, true
	}
	left := max(0, 100-w.UsedPct)
	var pace *WindowPace
	trusted := false
	if w.Duration > 0 && !w.ResetsAt.IsZero() {
		pace = w.PaceAt(now)
		elapsed := w.Duration - w.ResetsAt.Sub(now)
		trusted = pace != nil && float64(elapsed) >= pressureMinElapsed*float64(w.Duration)
	}
	if trusted && !pace.LastsToReset {
		before := w.ResetsAt.Sub(now) - pace.RunsOutIn
		if float64(before) >= pressureMinMargin*float64(w.Duration) {
			return before, true
		}
	}
	if left <= PressureFloorPct && (!trusted || !pace.LastsToReset) {
		return 0, true
	}
	return 0, false
}

// Pressure is evidence that a plan is running short: its tightest pressed
// window, what is left of it, and how far ahead of its reset it runs out.
type Pressure struct {
	// Vendor names the plan as its users do ("Claude").
	Vendor string `json:"vendor"`
	// Window is "5h" or "weekly".
	Window  string  `json:"window"`
	LeftPct float64 `json:"left_pct"`
	// RunsOutBeforeReset is how long before its reset the window runs out
	// at the pace so far; zero when only the floor pressed it.
	RunsOutBeforeReset time.Duration `json:"runs_out_before_reset_ns,omitempty"`
	ResetsAt           time.Time     `json:"resets_at,omitzero"`
	ObservedAt         time.Time     `json:"observed_at,omitzero"`
	FreshUntil         time.Time     `json:"fresh_until,omitzero"`
	// Source is where the reading came from (claude_code_statusline,
	// daemon_glance, or the vendor window's own source).
	Source string `json:"source,omitempty"`
}

// Why says, in the operator's words, why the plan counts as pressed.
func (p *Pressure) Why() string {
	if p == nil {
		return ""
	}
	if p.RunsOutBeforeReset > 0 {
		return fmt.Sprintf("%s %s runs out ~%s before it resets at this pace (%s left)",
			p.Vendor, p.Window, pressureSpan(p.RunsOutBeforeReset), pressureLeft(p.LeftPct))
	}
	return fmt.Sprintf("%s %s has %s left", p.Vendor, p.Window, pressureLeft(p.LeftPct))
}

// Evidence is the pressure as a ledger records it: the figures, where they
// came from and until when they hold.
func (p *Pressure) Evidence() string {
	if p == nil {
		return ""
	}
	s := fmt.Sprintf("plan_pressure: %s", p.Why())
	if p.Source != "" {
		s += "; source " + p.Source
	}
	if !p.ObservedAt.IsZero() {
		s += "; observed " + p.ObservedAt.UTC().Format(time.RFC3339)
	}
	if !p.FreshUntil.IsZero() {
		s += "; fresh until " + p.FreshUntil.UTC().Format(time.RFC3339)
	}
	return s
}

// Tighter reports whether p is the tighter of p and o: less left.
func (p *Pressure) Tighter(o *Pressure) bool { return o == nil || (p != nil && p.LeftPct < o.LeftPct) }

// WindowPressure is the tightest pressed window among a plan's 5h and
// weekly windows, nil when none is. Model-scoped and unnamed windows are
// left out: they bind one model, not the plan, and stale windows say
// nothing about now.
func WindowPressure(vendor string, windows []VendorWindow, now time.Time) *Pressure {
	var best *Pressure
	for _, w := range windows {
		label, ok := pressureLabels[w.Name]
		if !ok || w.Stale || (!w.ResetsAt.IsZero() && !w.ResetsAt.After(now)) {
			continue
		}
		before, pressed := w.Pressed(now)
		if !pressed {
			continue
		}
		p := &Pressure{
			Vendor: vendor, Window: label, LeftPct: max(0, 100-w.UsedPct), RunsOutBeforeReset: before,
			ResetsAt: w.ResetsAt, ObservedAt: w.ObservedAt, Source: w.Source,
		}
		if p.Tighter(best) {
			best = p
		}
	}
	return best
}

// pressureLabels names the plan-wide windows as a person says them.
var pressureLabels = map[string]string{"5h": "5h", "week": "weekly"}

func pressureLeft(v float64) string { return fmt.Sprintf("%d%%", int(math.Round(max(0, v)))) }

// pressureSpan renders a countdown compactly: 45m, 3h 10m, 2d 4h.
func pressureSpan(d time.Duration) string {
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
