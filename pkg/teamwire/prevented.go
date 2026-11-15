package teamwire

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Schema 5 adds what TokenOps prevented, per finished UTC week and
// mechanism: how many times it acted, the tokens it kept out where it
// knows them, and the dollars where they are priced. Never per session,
// repository or person; the server withholds a group below its floor as
// it does every other figure.

// The mechanisms a prevented row can name: the waste report's prevented
// side (internal/capability/avoidable).
const (
	MechanismReadGuard   = "read_guard"
	MechanismFmt         = "fmt"
	MechanismAutopilot   = "autopilot"
	MechanismBoundary    = "boundary"
	MechanismLoopBreaker = "loop_breaker"
	MechanismCompaction  = "compaction"
	MechanismLockout     = "lockout"
)

// Mechanisms lists every valid mechanism.
var Mechanisms = []string{MechanismReadGuard, MechanismFmt, MechanismAutopilot, MechanismBoundary,
	MechanismLoopBreaker, MechanismCompaction, MechanismLockout}

// ValidMechanism reports whether m is a mechanism a row may name.
func ValidMechanism(m string) bool { return slices.Contains(Mechanisms, m) }

// FinishedWeekLag is how long after a week ends it is final: the same
// three days the team view waits before it shows a week. A week is sent
// only once it is final, so it never changes afterwards.
const FinishedWeekLag = 3 * 24 * time.Hour

// MaxPrevented bounds the prevented rows in one upload: every mechanism
// for every week an upload can cover.
const MaxPrevented = (MaxDays/7 + 2) * 7

// PreventedWeek is one mechanism's prevented totals over one finished UTC
// week.
type PreventedWeek struct {
	// Week is the Monday that starts the UTC week.
	Week      string `json:"week"`
	Mechanism string `json:"mechanism"`
	// Count is how many times the mechanism acted in the week.
	Count int64 `json:"count"`
	// Tokens is what it kept out of the context or the output, when known.
	Tokens int64 `json:"tokens,omitempty"`
	// USD is the dollars prevented, absent when the mechanism is not priced.
	USD *float64 `json:"usd,omitempty"`
}

// FinishedWeek reports whether the week starting week (a Monday) is final
// at now: it ended at least FinishedWeekLag before.
func FinishedWeek(week, now time.Time) bool {
	return !week.AddDate(0, 0, 7).Add(FinishedWeekLag).After(now)
}

// Validate reports the first reason the row is malformed, or nil.
func (r PreventedWeek) Validate() error {
	if err := validWeek(r.Week); err != nil {
		return err
	}
	if !ValidMechanism(r.Mechanism) {
		return fmt.Errorf("mechanism %q: not a mechanism", r.Mechanism)
	}
	if r.Count <= 0 {
		return errors.New("count: a row counts at least one action")
	}
	if err := nonNegative(map[string]int64{"tokens": r.Tokens}); err != nil {
		return err
	}
	if r.USD != nil {
		if err := dollars(map[string]float64{"usd": *r.USD}); err != nil {
			return err
		}
	}
	return nil
}

func validWeek(w string) error {
	d, err := time.Parse(DayLayout, w)
	if err != nil {
		return fmt.Errorf("week %q is not a date", w)
	}
	if d.Weekday() != time.Monday {
		return fmt.Errorf("week %s: not a Monday", w)
	}
	return nil
}

// validatePrevented checks the schema 5 section: each listed week is a
// finished Monday, and each row names a listed week once per mechanism.
func (u Upload) validatePrevented() error {
	if u.Schema < SchemaPrevented {
		if len(u.PreventedWeeks)+len(u.Prevented) > 0 {
			return fmt.Errorf("schema %d carries no prevented figures", u.Schema)
		}
		return nil
	}
	weeks := make(map[string]bool, len(u.PreventedWeeks))
	for _, w := range u.PreventedWeeks {
		if err := validWeek(w); err != nil {
			return fmt.Errorf("prevented_weeks: %w", err)
		}
		start, _ := time.Parse(DayLayout, w)
		if !FinishedWeek(start, u.ComputedAt) {
			return fmt.Errorf("prevented_weeks: week %s is not finished %s before computed_at", w, FinishedWeekLag)
		}
		if weeks[w] {
			return fmt.Errorf("prevented_weeks: %s appears twice", w)
		}
		weeks[w] = true
	}
	return checkRows("prevented", u.Prevented, MaxPrevented, nil, func(r PreventedWeek) (string, string, error) {
		if err := r.Validate(); err != nil {
			return r.Week, "", err
		}
		if !weeks[r.Week] {
			return r.Week, "", fmt.Errorf("week %s is not in prevented_weeks", r.Week)
		}
		return r.Week, r.Week + "/" + r.Mechanism, nil
	})
}
