// Package windowshare estimates how much of a plan's rate-limit window a
// task will take, from the shares earlier tasks of the same kind took.
//
// The estimate is a belief built from evidence: each Sample is one
// measured task (the window's usage when it was prepared and when it was
// reviewed), and the estimate is the middle half of those measurements.
// It is never a promise, and below a minimum number of samples it is not
// offered at all, because a range drawn from two tasks reads as knowledge
// it is not.
//
// Scope names where the work happened, such as a repository or a
// project. It is an opaque label: nothing here assumes the work is
// software.
package windowshare

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// DefaultMinSamples is how many measured tasks an estimate needs before
// it is shown.
const DefaultMinSamples = 5

// Caveat travels with every estimate and every measured share.
const Caveat = "The share is read from the plan's usage meter at the start and end of the task, so other work in the same window counts toward it. Ranges are the middle half of past measurements, not a promise."

// Key identifies the history a task is compared against.
type Key struct {
	// Kind is the task class (see optimization/taskclass).
	Kind string
	// Scope is where the work happened, e.g. a repository. Empty means
	// unscoped.
	Scope    string
	Provider string
	// Window is the length of the plan window the share is of.
	Window time.Duration
}

// Sample is one task whose share of the window was measured.
type Sample struct {
	Key
	SharePct float64
	At       time.Time
}

// Status says whether an estimate is offered.
type Status string

// Estimate statuses.
const (
	StatusEstimated    Status = "estimated"
	StatusInsufficient Status = "insufficient_history"
)

// Basis says which history the estimate came from.
type Basis string

// Bases, most specific first.
const (
	BasisClassAndScope Basis = "task_class_and_scope"
	BasisClass         Basis = "task_class"
)

// EstimateResult is the expected share of the window, as a range.
type EstimateResult struct {
	Status     Status `json:"status"`
	Basis      Basis  `json:"basis,omitempty"`
	Samples    int    `json:"samples"`
	MinSamples int    `json:"min_samples"`
	// LowPct and HighPct bound the middle half of past shares; MedianPct
	// is their middle. All zero when the estimate is suppressed.
	LowPct    float64 `json:"low_pct,omitempty"`
	MedianPct float64 `json:"median_pct,omitempty"`
	HighPct   float64 `json:"high_pct,omitempty"`
}

// Estimated reports whether a range is offered.
func (e EstimateResult) Estimated() bool { return e.Status == StatusEstimated }

// Estimate builds the expected share for want from history. The class
// and scope together are preferred; when they hold too few samples the
// class across every scope is used, and below minSamples there either way
// the estimate is suppressed.
func Estimate(history []Sample, want Key, minSamples int) EstimateResult {
	if minSamples <= 0 {
		minSamples = DefaultMinSamples
	}
	basis, shares, n := pick(history, want, minSamples)
	out := EstimateResult{Status: StatusInsufficient, MinSamples: minSamples, Samples: n}
	if basis == "" {
		return out
	}
	return fill(out, basis, shares)
}

func fill(out EstimateResult, basis Basis, shares []float64) EstimateResult {
	sort.Float64s(shares)
	out.Status, out.Basis, out.Samples = StatusEstimated, basis, len(shares)
	out.LowPct = round1(quantile(shares, 0.25))
	out.MedianPct = round1(quantile(shares, 0.5))
	out.HighPct = round1(quantile(shares, 0.75))
	return out
}

// quantile interpolates linearly between the sorted values.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// Reading is the window's usage at one moment.
type Reading struct {
	Known   bool
	UsedPct float64
	At      time.Time
	// ResetsAt is when the window resets, zero when unknown.
	ResetsAt time.Time
}

// LeftPct is what remains of the window.
func (r Reading) LeftPct() float64 { return math.Max(0, 100-r.UsedPct) }

// ResetsIn is the time to the reset, zero when unknown or past.
func (r Reading) ResetsIn() time.Duration {
	if r.ResetsAt.IsZero() || !r.ResetsAt.After(r.At) {
		return 0
	}
	return r.ResetsAt.Sub(r.At)
}

// Action is what the estimate suggests.
type Action string

// Actions, least disruptive first.
const (
	// ActionNone — no estimate or no reading, so nothing to suggest.
	ActionNone Action = "none"
	// ActionProceed — the typical share fits in what is left.
	ActionProceed Action = "proceed"
	// ActionLighterTier — only the low end fits: a lighter model tier
	// stretches what is left.
	ActionLighterTier Action = "lighter_tier"
	// ActionDefer — not even the low end fits before the reset.
	ActionDefer Action = "defer"
)

// Advice is a suggestion, never an action taken.
type Advice struct {
	Action Action `json:"action"`
	Reason string `json:"reason"`
	// SuggestedTier names the model tier to try, when Action is
	// lighter_tier or defer and a lighter tier exists.
	SuggestedTier string `json:"suggested_tier,omitempty"`
	// SuggestedModel names a concrete model when routing advice already
	// proposed one.
	SuggestedModel string `json:"suggested_model,omitempty"`
}

// Advise compares the estimate with what is left of the window.
func Advise(est EstimateResult, now Reading) Advice {
	if !est.Estimated() {
		return Advice{Action: ActionNone, Reason: "no estimate yet for this kind of task"}
	}
	if !now.Known {
		return Advice{Action: ActionNone, Reason: "the plan window is not being measured, so the estimate cannot be compared with what is left"}
	}
	left := now.LeftPct()
	switch {
	case left >= est.HighPct:
		return Advice{Action: ActionProceed, Reason: fmt.Sprintf("tasks like this typically take up to %s%% and %s%% is left", pct(est.HighPct), pct(left))}
	case left >= est.LowPct:
		return Advice{Action: ActionLighterTier, Reason: fmt.Sprintf("%s%% is left and tasks like this take %s–%s%%: a lighter model tier, or a smaller slice of the task, makes it more likely to fit", pct(left), pct(est.LowPct), pct(est.HighPct))}
	}
	reason := fmt.Sprintf("tasks like this take at least %s%% and only %s%% is left", pct(est.LowPct), pct(left))
	if in := now.ResetsIn(); in > 0 {
		reason += "; defer it until the window resets in " + Duration(in)
	} else {
		reason += "; defer it until the window resets"
	}
	return Advice{Action: ActionDefer, Reason: reason}
}

// Summary is the one line an agent reads: the expected share, and what
// is left.
func Summary(est EstimateResult, now Reading, window time.Duration) string {
	var head string
	if est.Estimated() {
		head = fmt.Sprintf("typically %s–%s%% of your %s window", pct(est.LowPct), pct(est.HighPct), WindowLabel(window))
	} else {
		head = fmt.Sprintf("no estimate yet: %d of %d past tasks like this measured", est.Samples, est.MinSamples)
	}
	if !now.Known {
		return head + "; the window is not being measured"
	}
	tail := fmt.Sprintf("%s%% left", pct(now.LeftPct()))
	if in := now.ResetsIn(); in > 0 {
		tail += ", resets in " + Duration(in)
	}
	return head + "; " + tail
}

// Actual is the share a finished task took.
type Actual struct {
	Measured bool    `json:"measured"`
	SharePct float64 `json:"share_pct,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}

// Measure reads the share from the window at the start and end of a task.
// A reset in between, or usage that fell, leaves the share unknown rather
// than inventing one.
func Measure(start, end Reading) Actual {
	switch {
	case !start.Known:
		return Actual{Reason: "the window was not measured when the task was prepared"}
	case !end.Known:
		return Actual{Reason: "the window is not measured now"}
	case !start.ResetsAt.IsZero() && !end.At.Before(start.ResetsAt):
		return Actual{Reason: "the window reset during the task, so its share cannot be read"}
	case end.UsedPct < start.UsedPct:
		return Actual{Reason: "the window's usage fell during the task (a reset or a rolling window), so its share cannot be read"}
	}
	return Actual{Measured: true, SharePct: round1(end.UsedPct - start.UsedPct)}
}

// Directions an actual can fall relative to the estimate.
const (
	DirectionWithin = "within"
	DirectionOver   = "over"
	DirectionUnder  = "under"
)

// ScoreResult says how the estimate compared with the actual.
type ScoreResult struct {
	Scored    bool   `json:"scored"`
	Direction string `json:"direction,omitempty"`
	// ErrorPct is actual minus the estimate's median, in points.
	ErrorPct float64 `json:"error_pct,omitempty"`
}

// Score compares a measured actual with the estimate made before it.
func Score(est EstimateResult, actual Actual) ScoreResult {
	if !est.Estimated() || !actual.Measured {
		return ScoreResult{}
	}
	out := ScoreResult{Scored: true, Direction: DirectionWithin, ErrorPct: round1(actual.SharePct - est.MedianPct)}
	switch {
	case actual.SharePct > est.HighPct:
		out.Direction = DirectionOver
	case actual.SharePct < est.LowPct:
		out.Direction = DirectionUnder
	}
	return out
}

// WindowLabel names a window length the way plans state it: 5h, 7d.
func WindowLabel(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%dh", int(d.Round(time.Hour)/time.Hour))
}

// Duration renders a wait compactly: 45m, 1h10, 2d 3h.
func Duration(d time.Duration) string {
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02d", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func pct(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}
