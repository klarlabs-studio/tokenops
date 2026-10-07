package config

import (
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// PlanCovers reports whether provider's configured plan covers its usage
// (plans.Covers): bound, and not billed at API rates.
func (c Config) PlanCovers(provider string) bool { return plans.Covers(c.Plans[provider]) }

// PlanLimit is the operator-supplied side of a plan whose limit this tool
// cannot know: the org spend limit an admin set in the vendor console, and
// the rate a negotiated contract actually bills at.
type PlanLimit struct {
	// SpendLimitUSD is the org's configured cap for the window below.
	// Required for a spend-denominated plan; `plan set` refuses the
	// binding without it rather than defaulting to a number nobody chose.
	SpendLimitUSD float64 `yaml:"spend_limit_usd,omitempty"`
	// Window is the period the limit applies over: monthly (default),
	// weekly or daily, matching how the console states it.
	Window string `yaml:"window,omitempty"`
	// RateFactor scales measured spend to a negotiated rate.
	//
	// Enterprise contracts are frequently discounted off list while this
	// tool costs from the public rate card, so a console limit would
	// otherwise be compared against an overstatement — and the error is
	// invisible, which is the kind worth refusing to ship. Zero or one
	// means list price.
	RateFactor float64 `yaml:"rate_factor,omitempty"`
}
