package plans

import (
	"sort"
	"time"
)

// Binding is one change of a provider's plan: from From on, the provider
// is on Plan. An empty Plan means no plan (usage is billed per token).
type Binding struct {
	Provider string    `json:"provider"`
	Plan     string    `json:"plan"`
	From     time.Time `json:"from"`
	// Recorded is when the change was written, which differs from From
	// when a switch is backdated.
	Recorded time.Time `json:"recorded"`
}

// History is every recorded plan change.
//
// The configuration holds only the plan in force now, and a report over
// a past period needs the plan in force then: a switch from Plus to Pro
// in the middle of a month changes the month's plan cost and its
// limits. History answers that question per instant.
//
// A provider with no recorded change keeps the configured plan for all
// time, which is what every report assumed before history existed.
type History []Binding

// Period is a stretch of time on one plan.
type Period struct {
	Plan string    `json:"plan"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Days is the period's length in days.
func (p Period) Days() float64 { return p.To.Sub(p.From).Hours() / 24 }

// forProvider returns the provider's changes ordered by From, later
// recordings winning ties so a correction replaces what it corrects.
func (h History) forProvider(provider string) []Binding {
	var out []Binding
	for _, b := range h {
		if b.Provider == provider {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].From.Equal(out[j].From) {
			return out[i].From.Before(out[j].From)
		}
		return out[i].Recorded.Before(out[j].Recorded)
	})
	return out
}

// At is the provider's plan at t. current is the configured plan, used
// when the provider has no recorded change. Before the first recorded
// change the plan is unknown and At returns "".
func (h History) At(provider string, t time.Time, current string) string {
	bs := h.forProvider(provider)
	if len(bs) == 0 {
		return current
	}
	plan := ""
	for _, b := range bs {
		if b.From.After(t) {
			break
		}
		plan = b.Plan
	}
	return plan
}

// Periods splits [since, until) into stretches on one plan each,
// dropping stretches with no plan.
func (h History) Periods(provider string, since, until time.Time, current string) []Period {
	if !until.After(since) {
		return nil
	}
	bs := h.forProvider(provider)
	if len(bs) == 0 {
		if current == "" {
			return nil
		}
		return []Period{{Plan: current, From: since, To: until}}
	}
	// Boundaries inside the range, in order.
	cuts := []time.Time{since}
	for _, b := range bs {
		if b.From.After(since) && b.From.Before(until) {
			cuts = append(cuts, b.From)
		}
	}
	cuts = append(cuts, until)
	var out []Period
	for i := 0; i+1 < len(cuts); i++ {
		from, to := cuts[i], cuts[i+1]
		if !to.After(from) {
			continue
		}
		plan := h.At(provider, from, current)
		if plan == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Plan == plan && out[n-1].To.Equal(from) {
			out[n-1].To = to
			continue
		}
		out = append(out, Period{Plan: plan, From: from, To: to})
	}
	return out
}

// Switch returns the bindings to record for moving provider to plan
// from `from` on. When the provider has no history yet and was on a plan
// (previous), that plan is recorded as in force until the switch, so the
// time before it keeps the plan it actually had instead of becoming
// unknown.
func (h History) Switch(provider, previous, plan string, from, now time.Time) []Binding {
	var out []Binding
	if len(h.forProvider(provider)) == 0 && previous != "" && previous != plan {
		out = append(out, Binding{Provider: provider, Plan: previous, Recorded: now})
	}
	return append(out, Binding{Provider: provider, Plan: plan, From: from, Recorded: now})
}

// averageMonthDays converts a monthly price to a daily one: the Gregorian
// mean, so a period's cost does not depend on which month it falls in.
const averageMonthDays = 365.2425 / 12

// PeriodCost is a period's share of a monthly price.
func PeriodCost(monthlyUSD float64, p Period) float64 {
	return monthlyUSD * p.Days() / averageMonthDays
}
