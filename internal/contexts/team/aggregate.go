package team

import (
	"fmt"
	"time"
)

// DefaultMinGroupSize is the smallest number of people an aggregate is
// shown for. Below it, a team or repository total is one or two people's
// figures under another name, so it is withheld.
const DefaultMinGroupSize = 3

// Dimension is what an aggregate is grouped by.
type Dimension string

// The dimensions. Individuals are not one: drill-down is a separate,
// granted and recorded read.
const (
	ByTeam Dimension = "team"
	ByRepo Dimension = "repo"
	ByKind Dimension = "kind"
)

// ParseDimension reads a dimension name.
func ParseDimension(s string) (Dimension, error) {
	switch d := Dimension(s); d {
	case ByTeam, ByRepo, ByKind:
		return d, nil
	case "":
		return ByTeam, nil
	}
	return "", fmt.Errorf("by %q: want team, repo or kind", s)
}

// Period is the span figures are summed over.
type Period string

// The periods.
const (
	Day  Period = "day"
	Week Period = "week"
)

// ParsePeriod reads a period name.
func ParsePeriod(s string) (Period, error) {
	switch p := Period(s); p {
	case Day, Week:
		return p, nil
	case "":
		return Week, nil
	}
	return "", fmt.Errorf("period %q: want day or week", s)
}

// Start is the first day of the period day falls in: the day itself, or
// the Monday of its ISO week.
func (p Period) Start(day time.Time) time.Time {
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	if p != Week {
		return d
	}
	offset := (int(d.Weekday()) + 6) % 7 // Monday = 0
	return d.AddDate(0, 0, -offset)
}

// Totals are summed figures. Every field adds; rates come from Rates after
// adding, never from averaging rates.
type Totals struct {
	Sessions         int64   `json:"sessions"`
	Instructions     int64   `json:"instructions"`
	Turns            int64   `json:"turns"`
	ToolCalls        int64   `json:"tool_calls"`
	ActiveSeconds    float64 `json:"active_seconds"`
	FirstTry         int64   `json:"first_try"`
	Reworked         int64   `json:"reworked"`
	Interrupted      int64   `json:"interrupted"`
	Escalated        int64   `json:"escalated"`
	Rejected         int64   `json:"rejected"`
	Tokens           int64   `json:"tokens"`
	CostUSD          float64 `json:"cost_usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	UnpricedTurns    int64   `json:"unpriced_turns"`
}

// Add sums o into t.
func (t *Totals) Add(o Totals) {
	t.Sessions += o.Sessions
	t.Instructions += o.Instructions
	t.Turns += o.Turns
	t.ToolCalls += o.ToolCalls
	t.ActiveSeconds += o.ActiveSeconds
	t.FirstTry += o.FirstTry
	t.Reworked += o.Reworked
	t.Interrupted += o.Interrupted
	t.Escalated += o.Escalated
	t.Rejected += o.Rejected
	t.Tokens += o.Tokens
	t.CostUSD += o.CostUSD
	t.APIEquivalentUSD += o.APIEquivalentUSD
	t.UnpricedTurns += o.UnpricedTurns
}

// Rates are derived from Totals. A rate with no instructions under it is
// zero, and Instructions says so.
type Rates struct {
	FirstTryPct          float64 `json:"first_try_pct"`
	ReworkPct            float64 `json:"rework_pct"`
	InterruptPct         float64 `json:"interrupt_pct"`
	EscalationPct        float64 `json:"escalation_pct"`
	RejectionPct         float64 `json:"rejection_pct"`
	TurnsPerInstruction  float64 `json:"turns_per_instruction"`
	TokensPerInstruction float64 `json:"tokens_per_instruction"`
	// CostPerInstruction is at API list prices.
	CostPerInstruction float64 `json:"cost_per_instruction_usd"`
	MinutesPerInstr    float64 `json:"minutes_per_instruction"`
}

// Rates derives the rates from t.
func (t Totals) Rates() Rates {
	if t.Instructions == 0 {
		return Rates{}
	}
	n := float64(t.Instructions)
	pct := func(v int64) float64 { return float64(v) / n * 100 }
	return Rates{
		FirstTryPct:          pct(t.FirstTry),
		ReworkPct:            pct(t.Reworked),
		InterruptPct:         pct(t.Interrupted),
		EscalationPct:        pct(t.Escalated),
		RejectionPct:         pct(t.Rejected),
		TurnsPerInstruction:  float64(t.Turns) / n,
		TokensPerInstruction: float64(t.Tokens) / n,
		CostPerInstruction:   t.APIEquivalentUSD / n,
		MinutesPerInstr:      t.ActiveSeconds / 60 / n,
	}
}

// Row is one group's figures over one period.
type Row struct {
	PeriodStart time.Time
	// Group is the team name, repository label or kind of work.
	Group string
	// People is how many distinct members contributed.
	People int
	Totals Totals
}

// Retention bounds, in days.
const (
	DefaultRetentionDays      = 400
	MinRetentionDays          = 30
	MaxRetentionDays          = 3650
	DefaultAuditRetentionDays = 730
)

// RetentionCutoff is the first day still kept: figures for earlier days
// are deleted.
func RetentionCutoff(now time.Time, days int) time.Time {
	if days < MinRetentionDays {
		days = MinRetentionDays
	}
	if days > MaxRetentionDays {
		days = MaxRetentionDays
	}
	return Day.Start(now.UTC()).AddDate(0, 0, -days)
}
