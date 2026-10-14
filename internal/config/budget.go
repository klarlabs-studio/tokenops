package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/spend/allocation"
)

// BudgetConfig is one spend limit the watcher (active mode) and
// on-demand evaluations check. Window is a calendar window in UTC.
//
// A budget holds one window (Window with LimitUSD or LimitTokens, the
// form every budget had before) or several (Windows, e.g. $10 a day and
// $100 a month at once). The two forms do not mix.
type BudgetConfig struct {
	Name     string  `yaml:"name"`
	Window   string  `yaml:"window,omitempty"` // daily | weekly | monthly
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
	// Mode, Require and Stage are the single window's enforcement; see
	// BudgetWindowConfig.
	Mode    string `yaml:"mode,omitempty"`
	Require string `yaml:"require,omitempty"`
	Stage   string `yaml:"stage,omitempty"`
	// Tag scopes the limit to work carrying one allocation tag,
	// "key=value" such as "client=acme"; empty applies to all spend.
	Tag string `yaml:"tag,omitempty"`
	// Windows are the budget's windows when it holds several. Basis,
	// thresholds and scope above apply to each.
	Windows []BudgetWindowConfig `yaml:"windows,omitempty"`
}

// BudgetWindowConfig is one window of a budget: a calendar window, its
// ceiling, and whether reaching it notifies (soft, the default) or is
// enforced (hard).
//
// A hard window is enforced through the action boundaries (ADR 0014): it
// is a `spend` rule that requires Require (ask, the default, or deny)
// once the window is 100% used. Like every boundary it starts in shadow
// and binds only once `tokenops boundaries promote` sets Stage.
type BudgetWindowConfig struct {
	Window      string  `yaml:"window"`
	LimitUSD    float64 `yaml:"limit_usd,omitempty"`
	LimitTokens int64   `yaml:"limit_tokens,omitempty"`
	Mode        string  `yaml:"mode,omitempty"`
	Require     string  `yaml:"require,omitempty"`
	Stage       string  `yaml:"stage,omitempty"`
}

// Validate checks one budget on its own. Config.Validate runs it over every
// budget; UpsertBudget runs it on the merged budget before storing it, so an
// edit is refused with the same rule the daemon would apply at boot.
func (b BudgetConfig) Validate() error {
	if b.Name == "" {
		return errors.New("name is required")
	}
	basis := strings.ToLower(b.Basis)
	switch basis {
	case "", budget.BasisSpend, budget.BasisEquivalent, budget.BasisTokens:
	default:
		return fmt.Errorf("basis must be %q, %q, or %q, got %q",
			budget.BasisSpend, budget.BasisEquivalent, budget.BasisTokens, b.Basis)
	}
	if b.WarnAt < 0 || b.WarnAt > 1 || b.CritAt < 0 || b.CritAt > 1 {
		return errors.New("warn_at and crit_at must be in [0,1]")
	}
	if b.Tag != "" {
		if _, err := allocation.ParseScope(b.Tag); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
	}
	if len(b.Windows) == 0 {
		return b.single().validate(basis)
	}
	if b.Window != "" || b.LimitUSD != 0 || b.LimitTokens != 0 || b.Mode != "" || b.Require != "" || b.Stage != "" {
		return errors.New("with windows, set window, limits, mode, require and stage inside windows, not beside them")
	}
	seen := map[string]bool{}
	for i, w := range b.Windows {
		if err := w.validate(basis); err != nil {
			return fmt.Errorf("windows[%d]: %w", i, err)
		}
		key := strings.ToLower(w.Window)
		if seen[key] {
			return fmt.Errorf("windows[%d]: the %s window is set twice", i, key)
		}
		seen[key] = true
	}
	return nil
}

// validate checks one window. The limit is denominated in the basis's own
// unit: a token budget is configured with limit_tokens, everything else
// with limit_usd. Requiring the wrong one is how a flat-rate operator ends
// up budgeting against dollars they are never billed.
func (w BudgetWindowConfig) validate(basis string) error {
	switch strings.ToLower(w.Window) {
	case string(budget.WindowDaily), string(budget.WindowWeekly), string(budget.WindowMonthly):
	default:
		return fmt.Errorf("window must be daily, weekly, or monthly, got %q", w.Window)
	}
	if basis == budget.BasisTokens {
		if w.LimitTokens <= 0 {
			return fmt.Errorf("limit_tokens must be positive for basis %q, got %d",
				budget.BasisTokens, w.LimitTokens)
		}
	} else if w.LimitUSD <= 0 {
		return fmt.Errorf("limit_usd must be positive, got %g", w.LimitUSD)
	}
	mode := strings.ToLower(w.Mode)
	switch mode {
	case "", budget.ModeSoft, budget.ModeHard:
	default:
		return fmt.Errorf("mode must be soft or hard, got %q", w.Mode)
	}
	if mode != budget.ModeHard {
		if w.Require != "" || w.Stage != "" {
			return errors.New("require and stage apply to a hard window only (mode: hard)")
		}
		return nil
	}
	switch strings.ToLower(w.Require) {
	case "", string(boundaries.RequireAsk), string(boundaries.RequireDeny):
	default:
		return fmt.Errorf("require must be ask or deny, got %q", w.Require)
	}
	switch strings.ToLower(w.Stage) {
	case "", string(boundaries.StageShadow), string(boundaries.StageEnforce):
	default:
		return fmt.Errorf("stage must be shadow or enforce, got %q", w.Stage)
	}
	return nil
}

// BudgetLimits maps the configured budgets into the budget engine's
// domain type: one limit per window.
func (c Config) BudgetLimits() []budget.Limit {
	if len(c.Budgets) == 0 {
		return nil
	}
	out := make([]budget.Limit, 0, len(c.Budgets))
	for _, b := range c.Budgets {
		ws := b.EffectiveWindows()
		for _, w := range ws {
			window := budget.Window(strings.ToLower(w.Window))
			out = append(out, budget.Limit{
				Name:        b.Name,
				ID:          b.windowKey(window, len(ws)),
				Window:      window,
				LimitUSD:    w.LimitUSD,
				LimitTokens: w.LimitTokens,
				WarnAt:      b.WarnAt,
				CritAt:      b.CritAt,
				WorkflowID:  b.WorkflowID,
				AgentID:     b.AgentID,
				Basis:       strings.ToLower(b.Basis),
				Mode:        w.mode(),
				Tag:         normalizedTag(b.Tag),
			})
		}
	}
	return out
}

// normalizedTag is a budget's tag scope as the watcher reads it: the key
// spelled canonically. A malformed scope is kept, and refused by Validate.
func normalizedTag(tag string) string {
	if s, err := allocation.ParseScope(tag); err == nil {
		return s.String()
	}
	return tag
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
