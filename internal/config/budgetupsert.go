package config

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultBudgetWindow is the window a new budget gets when the caller
// names none.
const DefaultBudgetWindow = "monthly"

// ErrBudgetNoCeiling refuses a budget with nothing to trip on.
var ErrBudgetNoCeiling = errors.New("a budget needs a ceiling: limit_usd, or limit_tokens with basis tokens")

// BudgetUpdate is a caller's edit to one budget. A zero field means "not
// supplied": the upsert keeps what the stored budget already has, so an
// edit names only what it changes.
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
	if b.LimitUSD <= 0 && b.LimitTokens <= 0 {
		return BudgetUpsert{}, ErrBudgetNoCeiling
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

// mergeInto overwrites only the fields u supplies. Non-zero rather than
// positive counts as supplied, so a negative limit reaches validation and
// is refused instead of being mistaken for "leave it alone".
func (u BudgetUpdate) mergeInto(b *BudgetConfig) {
	if w := strings.TrimSpace(u.Window); w != "" {
		b.Window = strings.ToLower(w)
	}
	if u.LimitUSD != 0 {
		b.LimitUSD = u.LimitUSD
	}
	if u.LimitTokens != 0 {
		b.LimitTokens = u.LimitTokens
	}
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
