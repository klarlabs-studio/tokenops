package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
)

// BudgetConfig is one spend limit the watcher (active mode) and
// on-demand evaluations check. Window is a calendar window in UTC.
type BudgetConfig struct {
	Name     string  `yaml:"name"`
	Window   string  `yaml:"window"` // daily | weekly | monthly
	LimitUSD float64 `yaml:"limit_usd"`
	// LimitTokens is the ceiling for a `basis: tokens` budget. Required
	// for that basis and ignored otherwise. Flat-rate plans bill $0.00
	// at the margin, so this is the only limit that can trip for them.
	LimitTokens int64 `yaml:"limit_tokens"`
	// WarnAt / CritAt are fractional thresholds of the active limit;
	// zero falls back to the budget engine defaults (0.75 / 0.95).
	WarnAt float64 `yaml:"warn_at"`
	CritAt float64 `yaml:"crit_at"`
	// WorkflowID / AgentID optionally scope the limit to one workflow
	// or agent; empty applies to all spend.
	WorkflowID string `yaml:"workflow_id"`
	AgentID    string `yaml:"agent_id"`
	// Basis selects what the limit watches: "spend" (default — real
	// billed cost), "equivalent" (API list-price value, including
	// plan-covered usage), or "tokens" (raw token volume, paired with
	// limit_tokens). Flat-plan deployments want "tokens" for a real
	// ceiling, or "equivalent" for a list-price counterfactual — their
	// billed spend is always ~0.
	Basis string `yaml:"basis"`
}

// Validate checks one budget on its own. Config.Validate runs it over every
// budget; UpsertBudget runs it on the merged budget before storing it, so an
// edit is refused with the same rule the daemon would apply at boot.
func (b BudgetConfig) Validate() error {
	if b.Name == "" {
		return errors.New("name is required")
	}
	switch strings.ToLower(b.Window) {
	case string(budget.WindowDaily), string(budget.WindowWeekly), string(budget.WindowMonthly):
	default:
		return fmt.Errorf("window must be daily, weekly, or monthly, got %q", b.Window)
	}
	// The limit is denominated in the basis's own unit: a token
	// budget is configured with limit_tokens, everything else with
	// limit_usd. Requiring the wrong one is how a flat-rate operator
	// ends up budgeting against dollars they are never billed.
	switch basis := strings.ToLower(b.Basis); basis {
	case budget.BasisTokens:
		if b.LimitTokens <= 0 {
			return fmt.Errorf("limit_tokens must be positive for basis %q, got %d",
				budget.BasisTokens, b.LimitTokens)
		}
	case "", budget.BasisSpend, budget.BasisEquivalent:
		if b.LimitUSD <= 0 {
			return fmt.Errorf("limit_usd must be positive, got %g", b.LimitUSD)
		}
	default:
		return fmt.Errorf("basis must be %q, %q, or %q, got %q",
			budget.BasisSpend, budget.BasisEquivalent, budget.BasisTokens, b.Basis)
	}
	if b.WarnAt < 0 || b.WarnAt > 1 || b.CritAt < 0 || b.CritAt > 1 {
		return errors.New("warn_at and crit_at must be in [0,1]")
	}
	return nil
}

// BudgetLimits maps the configured budgets into the budget engine's
// domain type.
func (c Config) BudgetLimits() []budget.Limit {
	if len(c.Budgets) == 0 {
		return nil
	}
	out := make([]budget.Limit, 0, len(c.Budgets))
	for _, b := range c.Budgets {
		out = append(out, budget.Limit{
			Name:        b.Name,
			Window:      budget.Window(strings.ToLower(b.Window)),
			LimitUSD:    b.LimitUSD,
			LimitTokens: b.LimitTokens,
			WarnAt:      b.WarnAt,
			CritAt:      b.CritAt,
			WorkflowID:  b.WorkflowID,
			AgentID:     b.AgentID,
			Basis:       strings.ToLower(b.Basis),
		})
	}
	return out
}

// WatchConfig tunes the active-mode background watcher.
type WatchConfig struct {
	// Interval between watcher evaluations. Default 15m, minimum 1m.
	Interval time.Duration `yaml:"interval"`
}

// EffectiveInterval returns the watcher cadence with defaults applied.
func (w WatchConfig) EffectiveInterval() time.Duration {
	if w.Interval <= 0 {
		return 15 * time.Minute
	}
	if w.Interval < time.Minute {
		return time.Minute
	}
	return w.Interval
}
