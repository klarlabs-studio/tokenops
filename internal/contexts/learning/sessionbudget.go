package learning

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Where a session budget came from.
const (
	// SourceHistory is a budget read off the operator's own sessions.
	SourceHistory = "history"
	// SourceDefault is the fixed shipping budget, kept while there are
	// too few sessions to read one off.
	SourceDefault = "default"
)

// Which limit held a derived budget back from the observed percentile.
const (
	ClampFloor     = "floor"
	ClampCeiling   = "ceiling"
	ClampDriftUp   = "drift_up"
	ClampDriftDown = "drift_down"
)

// SessionCost is the evidence: one session's API-equivalent cost, and
// when it was last seen.
type SessionCost struct {
	Session  string
	USD      float64
	LastSeen time.Time
}

// BudgetPolicy bounds how a session budget may be read off history.
type BudgetPolicy struct {
	// Percentile of the session-cost distribution the budget sits at.
	Percentile float64
	// Window is how far back a session may have been seen to count.
	Window time.Duration
	// Refresh is how long a belief stands before it is re-derived.
	Refresh time.Duration
	// MinSessions is the evidence needed before history replaces the
	// default.
	MinSessions int
	// FloorUSD and CeilingUSD bound any derived budget.
	FloorUSD, CeilingUSD float64
	// MaxDrift is the largest share a derived budget may move from the
	// previous derived one in a single refresh. It is what stops a run
	// of wasteful weeks from quietly becoming the new normal.
	MaxDrift float64
	// DefaultUSD is the budget used while evidence is short.
	DefaultUSD float64
	// ExpectedPercentile replaces Percentile once at least MinAnswers
	// budget tips were answered and at least ExpectedShare of those
	// answers were "expected": the operator saying the bar sits too low.
	ExpectedPercentile float64
	MinAnswers         int
	ExpectedShare      float64
}

// AlertAnswers is the operator's answers to budget tips: how many they
// answered, and how many of those they called expected.
type AlertAnswers struct {
	Expected int
	Answered int
}

// DefaultBudgetPolicy is the shipping policy: the p90 of the last 30
// days, refreshed weekly, once 20 sessions are known, between $10 and
// $500, moving at most 25% a week.
func DefaultBudgetPolicy(defaultUSD float64) BudgetPolicy {
	return BudgetPolicy{
		Percentile:  0.90,
		Window:      30 * 24 * time.Hour,
		Refresh:     7 * 24 * time.Hour,
		MinSessions: 20,
		FloorUSD:    10,
		CeilingUSD:  500,
		MaxDrift:    0.25,
		DefaultUSD:  defaultUSD,

		ExpectedPercentile: 0.95,
		MinAnswers:         3,
		ExpectedShare:      0.5,
	}
}

// SessionBudgetBelief is what TokenOps believes a session of this
// operator's should cost, with the evidence that produced it. It is a
// belief, not configuration: re-derived as the evidence moves, and
// overridden by any budget the operator sets.
type SessionBudgetBelief struct {
	BudgetUSD float64 `json:"budget_usd"`
	Source    string  `json:"source"`
	// Percentile and ObservedUSD are what the history said before any
	// clamp; Clamp names the limit that moved it, empty when none did.
	Percentile  float64 `json:"percentile,omitempty"`
	ObservedUSD float64 `json:"observed_usd,omitempty"`
	Clamp       string  `json:"clamp,omitempty"`
	// MaxDrift is the most it may move from PreviousUSD in a refresh.
	MaxDrift float64 `json:"max_drift,omitempty"`
	// PreviousUSD is the derived budget this one moved from, zero when
	// there was none.
	PreviousUSD float64 `json:"previous_usd,omitempty"`
	// Sessions is how many priced sessions in the window were read;
	// MinSessions how many history needs, kept so the default can say
	// how far off it is.
	Sessions    int       `json:"sessions"`
	MinSessions int       `json:"min_sessions,omitempty"`
	WindowDays  int       `json:"window_days"`
	DerivedAt   time.Time `json:"derived_at"`
	// ExpectedAnswers and Answered are the operator's answers to budget
	// tips the derivation read: an outcome, not a setting.
	ExpectedAnswers int `json:"expected_answers,omitempty"`
	Answered        int `json:"answered,omitempty"`
}

// DeriveSessionBudget reads a session budget off the operator's own
// session costs. Sessions outside the window, and ones that cost
// nothing (unpriced or empty), are not evidence. prev is the belief it
// replaces, nil when there is none; only a history-derived prev anchors
// the drift clamp.
func DeriveSessionBudget(costs []SessionCost, prev *SessionBudgetBelief, p BudgetPolicy, now time.Time) SessionBudgetBelief {
	return DeriveSessionBudgetAnswered(costs, AlertAnswers{}, prev, p, now)
}

// DeriveSessionBudgetAnswered is DeriveSessionBudget that also reads the
// operator's answers to budget tips: when most were "expected", the
// budget sits at ExpectedPercentile instead.
func DeriveSessionBudgetAnswered(costs []SessionCost, answers AlertAnswers, prev *SessionBudgetBelief, p BudgetPolicy, now time.Time) SessionBudgetBelief {
	cutoff := now.Add(-p.Window)
	var usd []float64
	for _, c := range costs {
		if c.USD > 0 && !c.LastSeen.Before(cutoff) {
			usd = append(usd, c.USD)
		}
	}
	b := SessionBudgetBelief{
		Sessions: len(usd), MinSessions: p.MinSessions,
		WindowDays: int(p.Window / (24 * time.Hour)), DerivedAt: now,
		ExpectedAnswers: answers.Expected, Answered: answers.Answered,
	}
	if len(usd) < p.MinSessions || len(usd) == 0 {
		b.Source, b.BudgetUSD = SourceDefault, p.DefaultUSD
		return b
	}
	b.Source, b.Percentile = SourceHistory, p.Percentile
	if p.ExpectedPercentile > 0 && p.MinAnswers > 0 && answers.Answered >= p.MinAnswers &&
		float64(answers.Expected) >= p.ExpectedShare*float64(answers.Answered) {
		b.Percentile = p.ExpectedPercentile
	}
	b.ObservedUSD = nearestRank(usd, b.Percentile)
	budget := math.Round(b.ObservedUSD)
	if prev != nil && prev.Source == SourceHistory && prev.BudgetUSD > 0 && p.MaxDrift > 0 {
		b.PreviousUSD, b.MaxDrift = prev.BudgetUSD, p.MaxDrift
		if hi := math.Round(prev.BudgetUSD * (1 + p.MaxDrift)); budget > hi {
			budget, b.Clamp = hi, ClampDriftUp
		}
		if lo := math.Round(prev.BudgetUSD * (1 - p.MaxDrift)); budget < lo {
			budget, b.Clamp = lo, ClampDriftDown
		}
	}
	if budget < p.FloorUSD {
		budget, b.Clamp = p.FloorUSD, ClampFloor
	}
	if p.CeilingUSD > 0 && budget > p.CeilingUSD {
		budget, b.Clamp = p.CeilingUSD, ClampCeiling
	}
	b.BudgetUSD = budget
	return b
}

// nearestRank is the nearest-rank percentile: the smallest value at or
// above which at least q of the values lie.
func nearestRank(v []float64, q float64) float64 {
	sorted := append([]float64(nil), v...)
	sort.Float64s(sorted)
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

// Stale reports whether the belief is due to be re-derived. One from
// the future means the clock moved, and is re-derived too.
func (b SessionBudgetBelief) Stale(now time.Time, p BudgetPolicy) bool {
	if b.DerivedAt.IsZero() || b.DerivedAt.After(now) {
		return true
	}
	return now.Sub(b.DerivedAt) >= p.Refresh
}

// Provenance says in a phrase where the budget came from.
func (b SessionBudgetBelief) Provenance() string {
	if b.Source == SourceHistory {
		return fmt.Sprintf("your p%d session, from %d sessions", int(math.Round(b.Percentile*100)), b.Sessions)
	}
	return fmt.Sprintf("the default, until your own sessions are known (%d of %d)", b.Sessions, b.MinSessions)
}

// FromHistory reports whether the budget was read off the operator's
// sessions rather than being the default.
func (b SessionBudgetBelief) FromHistory() bool { return b.Source == SourceHistory }

// DriftClamped reports whether the weekly drift limit held the budget
// back from what the history said.
func (b SessionBudgetBelief) DriftClamped() bool {
	return b.Clamp == ClampDriftUp || b.Clamp == ClampDriftDown
}

// BoundClamped reports whether the floor or ceiling held it back.
func (b SessionBudgetBelief) BoundClamped() bool {
	return b.Clamp == ClampFloor || b.Clamp == ClampCeiling
}
