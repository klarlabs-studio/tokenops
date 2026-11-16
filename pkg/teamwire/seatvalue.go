package teamwire

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// Schema 6 adds what a seat is worth and where its week is headed (ADR
// 0012, Upload schema 6): the plan-covered usage of each plan day at API
// list price, the plan advice's list value and every option it priced,
// the plans bound on the machine, and a forward snapshot of each plan's
// longest window. Every string is an enumeration, a catalog plan or a
// date. Nothing about a 5-hour window, and no instant, crosses: a 5-hour
// window's reset tells when someone started working.

// Limits and vocabulary of the schema 6 sections.
const (
	// MaxPlanBindings and MaxPlanWindows bound the snapshots: one row per
	// provider.
	MaxPlanBindings = 32
	MaxPlanWindows  = 32
	// MaxAdviceOptions bounds an advice's options: a plan family's tiers,
	// each with metered overflow, and the API.
	MaxAdviceOptions = 12
	// MaxPeakPct bounds an option's fullest window: demand in a tier's
	// own windows, which for the smallest tier of a family is many times
	// its allowance.
	MaxPeakPct = 10000
	// MaxProjectedPct bounds a window's projection at its reset.
	MaxProjectedPct = 999
	// MaxShortHours bounds how long before its reset a window runs out,
	// and ShortHoursStep is the step it is rounded to.
	MaxShortHours  = 168
	ShortHoursStep = 6

	// WindowWeekly and WindowMonthly are the window kinds a forward
	// snapshot may describe: the longest kind a plan has. Monthly is for
	// plans with a monthly allowance; no catalog plan has one yet.
	WindowWeekly  = "weekly"
	WindowMonthly = "monthly"
	// BasisAverage and BasisRecent are the rate a projection reads: the
	// window's average so far, or the last hour's.
	BasisAverage = "average"
	BasisRecent  = "recent"
)

// windowSpan is how far ahead of the upload a window of each kind can
// reset.
var windowSpan = map[string]time.Duration{
	WindowWeekly:  7 * 24 * time.Hour,
	WindowMonthly: 31 * 24 * time.Hour,
}

// AdviceOption is one way a plan advice priced the period: a tier, a tier
// with its overflow metered, or the API at list price.
type AdviceOption struct {
	// Kind is plan, plan_overflow or api; Plan the tier, empty for the
	// API.
	Kind string `json:"kind"`
	Plan string `json:"plan,omitempty"`
	// MonthlyUSD is what the option costs per month; absent when its fee
	// is unknown.
	MonthlyUSD *float64 `json:"monthly_usd,omitempty"`
	// Lockouts and BlockedMinutes are how often and how long the option
	// would have run out over the period.
	Lockouts       int64 `json:"lockouts"`
	BlockedMinutes int64 `json:"blocked_minutes"`
	// PeakPct is the fullest the option's windows would have got, in
	// points of its allowance; absent when not measured.
	PeakPct *float64 `json:"peak_pct,omitempty"`
}

// PlanBinding is a plan bound on the machine: a seat, whether or not it
// was used. The section is replaced whole with each upload.
type PlanBinding struct {
	Provider string `json:"provider"`
	Plan     string `json:"plan"`
	// Since is the UTC date the plan history has the plan in force from;
	// absent when the history does not say.
	Since string `json:"since,omitempty"`
	// Shared is the member's own statement that the subscription is
	// shared with someone else (team.shared_plans).
	Shared bool `json:"shared,omitempty"`
}

// PlanWindow is where a plan's longest window is headed by its reset, as
// the member's own surfaces project it. The section is replaced whole
// with each upload.
type PlanWindow struct {
	Provider string `json:"provider"`
	// Window is weekly or monthly, never a 5-hour window.
	Window string `json:"window"`
	// UsedPct is the share used, in whole points.
	UsedPct int64 `json:"used_pct"`
	// ProjectedPctAtReset is the share the forecast reaches by the reset,
	// in whole points: above 100 it runs out first. Zero when too early.
	ProjectedPctAtReset int64 `json:"projected_pct_at_reset"`
	// RunsOut is true when it runs out before the reset; ShortHours how
	// long before, rounded to ShortHoursStep.
	RunsOut    bool  `json:"runs_out"`
	ShortHours int64 `json:"short_hours"`
	// ResetsOn is the UTC date of the reset.
	ResetsOn string `json:"resets_on"`
	// Basis is the rate the projection reads: average or recent.
	Basis string `json:"basis"`
	// TooEarly is a window too little of which has gone by to project.
	TooEarly bool `json:"too_early"`
}

// Validate reports the first reason the option is malformed for
// provider's advice, or nil.
func (o AdviceOption) Validate(provider string) error {
	if !slices.Contains(AdviceKinds, o.Kind) {
		return fmt.Errorf("kind %q: not an option kind", o.Kind)
	}
	switch {
	case o.Kind == "api" && o.Plan != "":
		return errors.New("plan: the API option names no plan")
	case o.Kind != "api" && !ValidPlan(provider, o.Plan):
		return fmt.Errorf("plan %q: not a catalog plan of %s", o.Plan, provider)
	}
	if o.MonthlyUSD != nil {
		if err := dollars(map[string]float64{"monthly_usd": *o.MonthlyUSD}); err != nil {
			return err
		}
	}
	if err := nonNegative(map[string]int64{"lockouts": o.Lockouts}); err != nil {
		return err
	}
	if o.BlockedMinutes < 0 || o.BlockedMinutes > MaxAdviceDays*minutesPerDay {
		return fmt.Errorf("blocked_minutes %d: outside 0 to %d, the longest period", o.BlockedMinutes, MaxAdviceDays*minutesPerDay)
	}
	if o.PeakPct != nil && (math.IsNaN(*o.PeakPct) || *o.PeakPct < 0 || *o.PeakPct > MaxPeakPct) {
		return fmt.Errorf("peak_pct: not between 0 and %d", MaxPeakPct)
	}
	return nil
}

// Validate reports the first reason the binding is malformed, or nil.
func (b PlanBinding) Validate() error {
	if !ValidPlan(b.Provider, b.Plan) {
		return fmt.Errorf("%s/%s: not a catalog plan of a registered provider", b.Provider, b.Plan)
	}
	if b.Since != "" {
		if err := validDay(b.Since); err != nil {
			return fmt.Errorf("since: %w", err)
		}
	}
	return nil
}

// Validate reports the first reason the window is malformed, or nil.
func (w PlanWindow) Validate() error {
	if !ValidProvider(w.Provider) {
		return fmt.Errorf("provider %q: not a provider in the registry", w.Provider)
	}
	if _, ok := windowSpan[w.Window]; !ok {
		return fmt.Errorf("window %q: not a window kind (weekly or monthly)", w.Window)
	}
	if w.Basis != BasisAverage && w.Basis != BasisRecent {
		return fmt.Errorf("basis %q: not a basis (average or recent)", w.Basis)
	}
	if err := validDay(w.ResetsOn); err != nil {
		return fmt.Errorf("resets_on: %w", err)
	}
	if w.UsedPct < 0 || w.UsedPct > 100 {
		return errors.New("used_pct: not between 0 and 100")
	}
	if w.ProjectedPctAtReset < 0 || w.ProjectedPctAtReset > MaxProjectedPct {
		return fmt.Errorf("projected_pct_at_reset: not between 0 and %d", MaxProjectedPct)
	}
	if w.ShortHours < 0 || w.ShortHours > MaxShortHours || w.ShortHours%ShortHoursStep != 0 {
		return fmt.Errorf("short_hours %d: not a step of %d from 0 to %d", w.ShortHours, ShortHoursStep, MaxShortHours)
	}
	switch {
	case w.TooEarly && (w.RunsOut || w.ProjectedPctAtReset != 0):
		return errors.New("too_early: a window too early to project has no projection")
	case w.RunsOut && w.ProjectedPctAtReset < 100:
		return errors.New("runs_out: a window projected to last does not run out")
	case !w.RunsOut && w.ProjectedPctAtReset > 100:
		return errors.New("runs_out: a window projected past its allowance runs out")
	case !w.RunsOut && w.ShortHours != 0:
		return errors.New("short_hours: only a window that runs out is short")
	}
	return nil
}

// seatValueCarried reports whether u carries any of schema 6's additions.
func (u Upload) seatValueCarried() bool {
	if len(u.PlanBindings)+len(u.PlanWindows) > 0 {
		return true
	}
	for _, d := range u.PlanDays {
		if d.PlanValueUSD != nil || d.PlanUnpricedTurns != 0 {
			return true
		}
	}
	for _, a := range u.PlanAdvice {
		if a.ListValueUSD != nil || a.Options != nil {
			return true
		}
	}
	return false
}

// validateSeats checks the schema 6 sections of a schema 6 upload: the
// bindings and the forward snapshot, whose dates are held to the upload's
// computed_at. The additions to plan days and plan advice are checked with
// their rows.
func (u Upload) validateSeats() error {
	if u.Schema < SchemaSeats {
		return nil // Validate refused any seat value already
	}
	computed := u.ComputedAt.UTC().Truncate(24 * time.Hour)
	if err := checkRows("plan_bindings", u.PlanBindings, MaxPlanBindings, nil, func(b PlanBinding) (string, string, error) {
		if err := b.Validate(); err != nil {
			return "", "", err
		}
		if since, _ := time.Parse(DayLayout, b.Since); b.Since != "" && since.After(computed) {
			return "", "", fmt.Errorf("since %s: after computed_at", b.Since)
		}
		return "", b.Provider, nil
	}); err != nil {
		return err
	}
	return checkRows("plan_windows", u.PlanWindows, MaxPlanWindows, nil, func(w PlanWindow) (string, string, error) {
		if err := w.Validate(); err != nil {
			return "", "", err
		}
		reset, _ := time.Parse(DayLayout, w.ResetsOn)
		if reset.Before(computed) || reset.After(computed.Add(windowSpan[w.Window])) {
			return "", "", fmt.Errorf("resets_on %s: not within a %s window of computed_at", w.ResetsOn, w.Window)
		}
		return "", w.Provider, nil
	})
}

// validateSeatAdditions checks what schema 6 adds to a plan day: the
// value is bounded dollars, and unpriced turns go with a value.
func (r PlanDay) validateSeatAdditions() error {
	if r.PlanValueUSD != nil {
		if err := dollars(map[string]float64{"plan_value_usd": *r.PlanValueUSD}); err != nil {
			return err
		}
	}
	if err := nonNegative(map[string]int64{"plan_unpriced_turns": r.PlanUnpricedTurns}); err != nil {
		return err
	}
	if r.PlanUnpricedTurns > 0 && r.PlanValueUSD == nil {
		return errors.New("plan_unpriced_turns: counted only beside a plan_value_usd")
	}
	return nil
}

// validateSeatAdditions checks what schema 6 adds to a plan advice: the
// list value is bounded dollars, and each option is one way of paying,
// named once.
func (r PlanAdvice) validateSeatAdditions() error {
	if r.ListValueUSD != nil {
		if err := dollars(map[string]float64{"list_value_usd": *r.ListValueUSD}); err != nil {
			return err
		}
	}
	return checkRows("options", r.Options, MaxAdviceOptions, nil, func(o AdviceOption) (string, string, error) {
		return "", o.Kind + "/" + o.Plan, o.Validate(r.Provider)
	})
}

// withoutSeatValue is u's plan days and plan advice without schema 6's
// additions, copied so the caller's rows are left as they are.
func (u Upload) withoutSeatValue() Upload {
	u.PlanBindings, u.PlanWindows = nil, nil
	if u.PlanDays != nil {
		days := make([]PlanDay, len(u.PlanDays))
		for i, d := range u.PlanDays {
			d.PlanValueUSD, d.PlanUnpricedTurns = nil, 0
			days[i] = d
		}
		u.PlanDays = days
	}
	if u.PlanAdvice != nil {
		advice := make([]PlanAdvice, len(u.PlanAdvice))
		for i, a := range u.PlanAdvice {
			a.ListValueUSD, a.Options = nil, nil
			advice[i] = a
		}
		u.PlanAdvice = advice
	}
	return u
}
