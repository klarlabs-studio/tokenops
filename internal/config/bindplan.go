package config

import (
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// PlanBinding is what binding a provider to a plan changed.
type PlanBinding struct {
	Plan string
	// Previous is the plan bound before, empty when there was none.
	Previous string
	// RenamedFrom is the legacy name the caller gave, when it was an alias.
	RenamedFrom string
}

// BindPlan binds provider to planName in c, recording lim for a plan billed
// at API rates. The terminal's `plan set` and the MCP tool both go through
// it, so the two cannot accept different bindings.
//
// A spend-denominated plan has no vendor window, so its denominator is a
// spend limit — given in lim, or reported by the Claude usage meter. The
// binding is refused with neither rather than measured against a number
// nobody chose.
func (c *Config) BindPlan(provider, planName string, lim PlanLimit) (PlanBinding, error) {
	if err := plans.Validate(planName); err != nil {
		return PlanBinding{}, err
	}
	var b PlanBinding
	if modern, aliased := plans.ResolveAlias(planName); aliased {
		b.RenamedFrom, planName = planName, modern
	}
	meterReportsLimit := provider == "anthropic" && c.VendorUsage.ClaudeUsageMeter.Enabled
	if err := plans.ValidateSpendLimit(planName, lim.SpendLimitUSD, meterReportsLimit); err != nil {
		return PlanBinding{}, err
	}
	if lim.SpendLimitUSD > 0 || lim.RateFactor > 0 || lim.Window != "" {
		if c.PlanLimits == nil {
			c.PlanLimits = map[string]PlanLimit{}
		}
		pl := c.PlanLimits[provider]
		if lim.SpendLimitUSD > 0 {
			pl.SpendLimitUSD = lim.SpendLimitUSD
		}
		if lim.Window != "" {
			pl.Window = lim.Window
		}
		if lim.RateFactor > 0 {
			pl.RateFactor = lim.RateFactor
		}
		c.PlanLimits[provider] = pl
	}
	if c.Plans == nil {
		c.Plans = map[string]string{}
	}
	b.Previous = c.Plans[provider]
	c.Plans[provider] = planName
	b.Plan = planName
	return b, nil
}
