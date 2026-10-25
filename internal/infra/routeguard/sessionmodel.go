package routeguard

import (
	"slices"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SessionModelTarget is the model new sessions should start on while the
// plan is pressed: one tier below current, never below the balanced tier
// (the main session takes every kind of work, deep work included), from
// the offered models (already filtered by the model policy), named by an
// alias Claude Code accepts. ok is false when nothing qualifies.
func SessionModelTarget(current string, provider eventschema.Provider, catalog *modeltier.Catalog, candidates []string) (target, alias string, ok bool) {
	if catalog == nil || len(candidates) == 0 || strings.TrimSpace(current) == "" {
		return "", "", false
	}
	cat := catalog.WithCandidates(withCurrent(candidates, current))
	cur := cat.Resolve(provider, current).Tier
	want := modeltier.TierUnknown
	switch cur {
	case modeltier.TierDeep:
		want = modeltier.TierDefault
	case modeltier.TierDefault:
		want = modeltier.TierBalanced
	}
	if want == modeltier.TierUnknown {
		return "", "", false
	}
	target, ok = cat.Target(provider, want)
	if !ok || target == current || aliasFor(target) == "" || !slices.Contains(candidates, target) {
		return "", "", false
	}
	return target, aliasFor(target), true
}
