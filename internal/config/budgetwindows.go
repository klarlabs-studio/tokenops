package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
)

// hardBudgetAt is the share of a hard window's limit at which its boundary
// applies: the limit itself.
const hardBudgetAt = ">= 100"

// EffectiveWindows is the budget's windows in one form: the single window
// of a budget written the old way, or its windows list. The result is a
// copy.
func (b BudgetConfig) EffectiveWindows() []BudgetWindowConfig {
	if len(b.Windows) > 0 {
		return slices.Clone(b.Windows)
	}
	return []BudgetWindowConfig{b.single()}
}

// HasHardWindow reports whether any of the budget's windows is enforced
// through a boundary.
func (b BudgetConfig) HasHardWindow() bool {
	for _, w := range b.EffectiveWindows() {
		if w.mode() == budget.ModeHard {
			return true
		}
	}
	return false
}

func (b BudgetConfig) single() BudgetWindowConfig {
	return BudgetWindowConfig{
		Window: b.Window, LimitUSD: b.LimitUSD, LimitTokens: b.LimitTokens,
		Mode: b.Mode, Require: b.Require, Stage: b.Stage,
	}
}

// setWindows stores ws in the budget's shortest form: one window the old
// way, so a budget that never had a second window reads and writes as it
// always did, and several as a windows list.
func (b *BudgetConfig) setWindows(ws []BudgetWindowConfig) {
	if len(ws) == 1 {
		w := ws[0]
		b.Window, b.LimitUSD, b.LimitTokens = w.Window, w.LimitUSD, w.LimitTokens
		b.Mode, b.Require, b.Stage = w.Mode, w.Require, w.Stage
		b.Windows = nil
		return
	}
	b.Window, b.LimitUSD, b.LimitTokens = "", 0, 0
	b.Mode, b.Require, b.Stage = "", "", ""
	b.Windows = ws
}

// windowKey is the key a window's figure and alerts go by: the budget's
// name while it has one window, so a single-window budget keeps the key it
// always had, and "name:window" when it has several.
func (b BudgetConfig) windowKey(w budget.Window, windows int) string {
	if windows <= 1 {
		return b.Name
	}
	return budget.WindowKey(b.Name, w)
}

func (w BudgetWindowConfig) mode() string {
	if strings.EqualFold(w.Mode, budget.ModeHard) {
		return budget.ModeHard
	}
	return budget.ModeSoft
}

func (w BudgetWindowConfig) require() string {
	if r := strings.ToLower(w.Require); r != "" {
		return r
	}
	return string(boundaries.RequireAsk)
}

// BudgetRuleName is the name of the boundary a hard window is enforced
// by: budget-<budget>-<window>, in the characters a rule name allows.
func BudgetRuleName(budgetName, window string) string {
	window = strings.ToLower(window)
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(budgetName) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		switch {
		case ok:
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	// The whole name stays within a rule name's 64 characters.
	if room := 64 - len("budget-") - len(window) - 1; len(slug) > room {
		slug = strings.TrimRight(slug[:room], "-")
	}
	if slug == "" {
		return "budget-" + window
	}
	return "budget-" + slug + "-" + window
}

// BudgetRules are the boundaries the hard budget windows are enforced by
// (ADR 0014): for each, a spend rule on that window's share used, at its
// limit, requiring ask or deny, in the stage written beside the window.
// They are rules like any other: shadow first, promoted by name, listed by
// `tokenops boundaries status`.
func (c Config) BudgetRules() []boundaries.RuleSpec {
	var out []boundaries.RuleSpec
	for _, b := range c.Budgets {
		ws := b.EffectiveWindows()
		for _, w := range ws {
			if w.mode() != budget.ModeHard {
				continue
			}
			window := budget.Window(strings.ToLower(w.Window))
			out = append(out, boundaries.RuleSpec{
				Name:   BudgetRuleName(b.Name, string(window)),
				Action: string(boundaries.ActionSpend),
				When: map[string]string{
					string(boundaries.FieldBudgetUsed): hardBudgetAt,
					"budget":                           b.windowKey(window, len(ws)),
				},
				Require: w.require(),
				Stage:   strings.ToLower(w.Stage),
			})
		}
	}
	return out
}

// boundarySpecs are the boundaries section and the hard budget windows'
// rules, which are validated and evaluated together: a hard window whose
// rule name clashes with a written rule is refused.
func (c Config) boundarySpecs() []boundaries.RuleSpec {
	return append(slices.Clone(c.Boundaries), c.BudgetRules()...)
}

// SetBudgetRuleStage sets the stage of the hard window whose boundary is
// named rule, and reports whether there was one.
func (c *Config) SetBudgetRuleStage(rule, stage string) bool {
	for i := range c.Budgets {
		b := &c.Budgets[i]
		ws := b.EffectiveWindows()
		for j := range ws {
			if ws[j].mode() != budget.ModeHard || BudgetRuleName(b.Name, ws[j].Window) != rule {
				continue
			}
			ws[j].Stage = stage
			b.setWindows(ws)
			return true
		}
	}
	return false
}

// RemoveBudgetWindow deletes one window of the budget name and reports
// whether it existed. The last window is not removed: that is removing the
// budget.
func (c *Config) RemoveBudgetWindow(name, window string) (bool, error) {
	idx := c.budgetIndex(name)
	if idx < 0 {
		return false, nil
	}
	window = strings.ToLower(strings.TrimSpace(window))
	ws := c.Budgets[idx].EffectiveWindows()
	j := windowIndex(ws, window)
	if j < 0 {
		return false, nil
	}
	if len(ws) == 1 {
		return false, fmt.Errorf("the %s window is budget %q's only window: remove the budget instead", window, name)
	}
	c.Budgets[idx].setWindows(slices.Delete(ws, j, j+1))
	return true, nil
}

func windowIndex(ws []BudgetWindowConfig, window string) int {
	for i, w := range ws {
		if strings.EqualFold(w.Window, window) {
			return i
		}
	}
	return -1
}

func windowNames(ws []BudgetWindowConfig) string {
	names := make([]string, len(ws))
	for i, w := range ws {
		names[i] = w.Window
	}
	return strings.Join(names, ", ")
}

// errNoWindowNamed is an edit to a window-level field of a budget with
// several windows that does not say which.
var errNoWindowNamed = errors.New("name the one to change")
