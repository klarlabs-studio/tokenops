package planswitch

import "go.klarlabs.de/tokenops/internal/contexts/spend/plans"

// Plan is a subscription plan TokenOps knows: its price, windows and caps.
type Plan = plans.Plan

// Switches is a provider's recorded plan changes, oldest first.
type Switches = plans.History

// Lookup is the catalog's plan by name.
func Lookup(name string) (Plan, bool) { return plans.Lookup(name) }

// Catalog is every plan TokenOps knows, in name order.
func Catalog() []Plan {
	names := plans.Names()
	out := make([]Plan, 0, len(names))
	for _, name := range names {
		p, _ := plans.Lookup(name)
		out = append(out, p)
	}
	return out
}
