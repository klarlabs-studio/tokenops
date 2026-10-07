package routeguard

import (
	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// Kind is a kind of work the guard advises on: mechanical, reasoning, …
type Kind = taskclass.Kind

// Catalog places each model in a price tier.
type Catalog = modeltier.Catalog

// KindsOf is the kinds config names, as the guard compares them.
func KindsOf(names []string) []Kind {
	out := make([]Kind, 0, len(names))
	for _, n := range names {
		out = append(out, Kind(n))
	}
	return out
}

// NewCatalog tiers the models table prices.
func NewCatalog(table spend.Table) *Catalog { return modeltier.New(table, nil) }
