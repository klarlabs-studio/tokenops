package config

import (
	"errors"
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
)

// DefaultBudgetWindow is the window a new budget gets when the caller
// names none.
const DefaultBudgetWindow = "monthly"

// ErrBudgetNoCeiling refuses a budget with nothing to trip on.
var ErrBudgetNoCeiling = errors.New("a budget needs a ceiling: limit_usd, or limit_tokens with basis tokens")

// BudgetUpdate is a caller's edit to one budget. A zero field means "not
// supplied": the upsert keeps what the stored budget already has, so an
// edit names only what it changes.
//
// Window, the limits, Mode and Require belong to one window of the
// budget. On a budget with one window they edit it, and naming another
// window moves it there. On a budget with several, Window names the one
// to edit. AddWindow adds Window as a further window.
type BudgetUpdate struct {
	Name        string
	Window      string
	LimitUSD    float64
	LimitTokens int64
	WarnAt      float64
	CritAt      float64
	WorkflowID  string
	AgentID     string
	Basis       string
	// Mode is soft (notify) or hard (enforced through a spend boundary).
	Mode string
	// Require is what a hard window requires at its limit: ask or deny.
	Require string
	// AddWindow adds Window to the budget rather than editing one.
	AddWindow bool
}

// BudgetUpsert is what an upsert wrote.
type BudgetUpsert struct {
	Budget  BudgetConfig
	Created bool
}

// UpsertBudget creates the budget u names or merges u onto the existing
// one. The terminal's `budget set` and the MCP tool both go through it, so
// the two cannot accept different budgets.
//
// It merges rather than replaces because the MCP tool used to rebuild the
// whole budget from its inputs: an agent moving warn_at on the operator's
// `basis: tokens` budget silently dropped limit_tokens and basis, erasing
// the only ceiling a flat-rate plan can trip.
//
// The merged budget is validated before c changes, so a rejected edit
// leaves c as it was.
func (c *Config) UpsertBudget(u BudgetUpdate) (BudgetUpsert, error) {
	name := strings.TrimSpace(u.Name)
	if name == "" {
		return BudgetUpsert{}, errors.New("budget name is required")
	}
	idx := c.budgetIndex(name)
	b := BudgetConfig{Name: name, Window: DefaultBudgetWindow}
	if idx >= 0 {
		b = c.Budgets[idx]
	}
	u.mergeInto(&b)
	ws := b.EffectiveWindows()
	i, err := u.target(name, ws, idx < 0)
	if err != nil {
		return BudgetUpsert{}, err
	}
	if i == len(ws) {
		ws = append(ws, BudgetWindowConfig{})
	}
	if i >= 0 {
		u.mergeWindowInto(&ws[i])
	}
	b.setWindows(ws)
	for _, w := range ws {
		if w.LimitUSD <= 0 && w.LimitTokens <= 0 {
			return BudgetUpsert{}, ErrBudgetNoCeiling
		}
	}
	if err := b.Validate(); err != nil {
		return BudgetUpsert{}, fmt.Errorf("budget %q: %w", name, err)
	}
	if idx >= 0 {
		c.Budgets[idx] = b
		return BudgetUpsert{Budget: b}, nil
	}
	c.Budgets = append(c.Budgets, b)
	return BudgetUpsert{Budget: b, Created: true}, nil
}

// target is the index in ws of the window u edits: len(ws) for a window
// to add, and -1 when u changes only the budget-wide fields.
func (u BudgetUpdate) target(name string, ws []BudgetWindowConfig, created bool) (int, error) {
	window := strings.ToLower(strings.TrimSpace(u.Window))
	if u.AddWindow && !created {
		if window == "" {
			return 0, fmt.Errorf("budget %q: name the window to add", name)
		}
		if windowIndex(ws, window) >= 0 {
			return 0, fmt.Errorf("budget %q already has a %s window", name, window)
		}
		return len(ws), nil
	}
	if window != "" {
		if j := windowIndex(ws, window); j >= 0 {
			return j, nil
		}
	}
	if len(ws) == 1 {
		return 0, nil
	}
	if window != "" {
		return 0, fmt.Errorf("budget %q has no %s window (it has %s); add it as a further window", name, window, windowNames(ws))
	}
	if u.windowFields() {
		return 0, fmt.Errorf("budget %q has several windows (%s): %w", name, windowNames(ws), errNoWindowNamed)
	}
	return -1, nil
}

func (u BudgetUpdate) windowFields() bool {
	return u.LimitUSD != 0 || u.LimitTokens != 0 || strings.TrimSpace(u.Mode) != "" || strings.TrimSpace(u.Require) != ""
}

// RemoveBudget deletes the budget with name and reports whether one existed.
func (c *Config) RemoveBudget(name string) bool {
	idx := c.budgetIndex(name)
	if idx < 0 {
		return false
	}
	c.Budgets = append(c.Budgets[:idx], c.Budgets[idx+1:]...)
	return true
}

func (c *Config) budgetIndex(name string) int {
	for i, b := range c.Budgets {
		if b.Name == name {
			return i
		}
	}
	return -1
}

// mergeInto overwrites only the budget-wide fields u supplies. Non-zero
// rather than positive counts as supplied, so a negative threshold reaches
// validation and is refused instead of being mistaken for "leave it alone".
func (u BudgetUpdate) mergeInto(b *BudgetConfig) {
	if u.WarnAt != 0 {
		b.WarnAt = u.WarnAt
	}
	if u.CritAt != 0 {
		b.CritAt = u.CritAt
	}
	if u.WorkflowID != "" {
		b.WorkflowID = u.WorkflowID
	}
	if u.AgentID != "" {
		b.AgentID = u.AgentID
	}
	if basis := strings.TrimSpace(u.Basis); basis != "" {
		b.Basis = strings.ToLower(basis)
	}
}

// mergeWindowInto overwrites only the window fields u supplies. A window
// that turns soft drops its require and stage, so turning it hard again
// starts over in shadow, as every new boundary does.
func (u BudgetUpdate) mergeWindowInto(w *BudgetWindowConfig) {
	if v := strings.TrimSpace(u.Window); v != "" {
		w.Window = strings.ToLower(v)
	}
	if u.LimitUSD != 0 {
		w.LimitUSD = u.LimitUSD
	}
	if u.LimitTokens != 0 {
		w.LimitTokens = u.LimitTokens
	}
	switch mode := strings.ToLower(strings.TrimSpace(u.Mode)); mode {
	case "":
	case budget.ModeSoft:
		w.Mode, w.Require, w.Stage = "", "", ""
	default:
		w.Mode = mode
	}
	if r := strings.TrimSpace(u.Require); r != "" {
		w.Require = strings.ToLower(r)
	}
}
