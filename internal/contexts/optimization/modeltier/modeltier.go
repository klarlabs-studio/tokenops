// Package modeltier places a model on a capability tier so routing can
// be decided in terms of the work rather than in terms of model names.
//
// Routing wants to answer "this turn is a lookup, what should run it" for
// every provider an operator uses, not just the one whose model names
// happen to be hardcoded. The tier is that portable middle term: a task
// kind resolves to a tier, and each provider resolves the tier to
// whatever it actually offers.
//
// Tiers come from price rank inside a provider, because that is the
// signal that maintains itself. The rate card refreshes daily; a list of
// model names written into the source rots with every release, and a
// stale list is worse than none — it routes confidently to a model that
// no longer means what it meant.
//
// Where price is unavailable the resolution degrades in named steps
// rather than guessing silently, and every answer says which step it
// came from so a caller can decide how much to trust it. A model nobody
// can place stays Unknown, and routing leaves it alone.
package modeltier

import (
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Tier is a coarse capability band, ordered cheapest to most capable.
type Tier string

// The tiers. Unknown means the catalog declined to place the model.
const (
	TierUnknown  Tier = "unknown"
	TierLookup   Tier = "lookup"
	TierBalanced Tier = "balanced"
	TierDefault  Tier = "default"
	TierDeep     Tier = "deep"
)

// Basis names how a tier was decided, strongest evidence first. It
// travels with the answer so a caller can require, say, card-backed
// evidence before moving real work.
type Basis string

// The resolution steps.
const (
	BasisOverride   Basis = "override"   // the operator said so
	BasisCard       Basis = "card"       // priced directly by the rate card
	BasisNormalised Basis = "normalised" // same model, different provider prefix
	BasisLocal      Basis = "local"      // runs on this machine, costs nothing
	BasisFree       Basis = "free"       // vendor's own free tier
	BasisFamily     Basis = "family"     // recognised only by name
	BasisNone       Basis = "none"       // not placed
)

// Resolution is where a model landed and why.
type Resolution struct {
	Tier  Tier
	Basis Basis
	// CostPerMillion is input+output per million tokens, zero when free
	// or unknown — read Free and Basis to tell those apart.
	CostPerMillion float64
	// Free marks a model with no marginal cost: a vendor free tier or a
	// local runtime. These are the best targets for cheap work and the
	// old router could not see them, because it filtered on a cost
	// greater than zero and theirs is exactly zero.
	Free bool
}

// Catalog resolves models against a rate table plus operator overrides.
type Catalog struct {
	table     spend.Table
	overrides map[string]Tier
	// byName indexes the card by bare, normalised model name so the same
	// model reached through a proxy provider still prices.
	byName map[string]spend.Key
	// candidates, when set, restricts tier ranking to the models an
	// operator can actually choose. The card keeps retired models and
	// they sit at both ends of the price range, so ranking across the
	// whole catalogue pushes the live generation into the middle.
	candidates map[string]struct{}
}

// WithCandidates restricts tier ranking to the given bare model names.
// Resolution still prices anything the card knows; only the banding
// changes, because "expensive" should mean expensive among the models on
// offer today rather than among every model the vendor ever sold.
//
// An empty or nil list leaves ranking across the full card, which is the
// right behaviour for a caller that does not know the live set.
func (c *Catalog) WithCandidates(models []string) *Catalog {
	if c == nil || len(models) == 0 {
		return c
	}
	set := make(map[string]struct{}, len(models))
	for _, m := range models {
		set[normalise(m)] = struct{}{}
	}
	c.candidates = set
	return c
}

// ranks reports whether a model participates in tier banding.
func (c *Catalog) ranks(model string) bool {
	if len(c.candidates) == 0 {
		return true
	}
	_, ok := c.candidates[normalise(strings.TrimSuffix(model, "*"))]
	return ok
}

// New builds a Catalog. Overrides are keyed "provider/model" and win over
// every derived answer.
func New(table spend.Table, overrides map[string]Tier) *Catalog {
	c := &Catalog{table: table, overrides: overrides, byName: map[string]spend.Key{}}
	for k := range table.Rates {
		n := normalise(strings.TrimSuffix(k.Model, "*"))
		// First writer wins, and keys are visited in map order, so pick
		// deterministically: the shortest provider name, then the
		// lexically smaller one. Without this the index would differ run
		// to run and so would the prices it resolves.
		if prev, ok := c.byName[n]; ok && !preferKey(k, prev) {
			continue
		}
		c.byName[n] = k
	}
	return c
}

func preferKey(a, b spend.Key) bool {
	if len(a.Provider) != len(b.Provider) {
		return len(a.Provider) < len(b.Provider)
	}
	return a.Provider < b.Provider
}

// lookupTokens and deepTokens are the name fragments that still carry
// meaning when the card has never heard of a model. Deliberately short:
// a fragment that matches loosely would place a model confidently on the
// strength of a coincidence.
var (
	lookupTokens = []string{"haiku", "flash", "mini", "lite", "small", "air", "nano", "oss"}
	deepTokens   = []string{"opus", "fable", "mythos", "ultra", "thinking"}
)

// localProviders run on the operator's own machine.
var localProviders = map[string]bool{"ollama": true, "llamacpp": true, "lmstudio": true}

// Resolve places one model.
func (c *Catalog) Resolve(provider eventschema.Provider, model string) Resolution {
	if c == nil || model == "" {
		return Resolution{Tier: TierUnknown, Basis: BasisNone}
	}
	if t, ok := c.overrides[string(provider)+"/"+model]; ok {
		return Resolution{Tier: t, Basis: BasisOverride}
	}
	if localProviders[strings.ToLower(string(provider))] {
		return Resolution{Tier: TierLookup, Basis: BasisLocal, Free: true}
	}
	if isFree(model) {
		return Resolution{Tier: TierLookup, Basis: BasisFree, Free: true}
	}
	// Lookup, not a map index: the card stores snapshot rows as prefix
	// keys ("claude-sonnet-5*") so a version-suffixed request still
	// resolves. Indexing Rates directly matches only the handful of
	// exact rows and silently misses every refreshed price.
	if rate, err := c.table.Lookup(provider, model); err == nil {
		cost := rate.InputPerMillion + rate.OutputPerMillion
		return Resolution{Tier: c.tierForCost(provider, cost), Basis: BasisCard, CostPerMillion: cost}
	}
	if k, ok := c.byName[normalise(model)]; ok {
		if rate, err := c.table.Lookup(k.Provider, strings.TrimSuffix(k.Model, "*")); err == nil {
			cost := rate.InputPerMillion + rate.OutputPerMillion
			return Resolution{Tier: c.tierForCost(k.Provider, cost), Basis: BasisNormalised, CostPerMillion: cost}
		}
	}
	if t, ok := familyTier(model); ok {
		return Resolution{Tier: t, Basis: BasisFamily}
	}
	return Resolution{Tier: TierUnknown, Basis: BasisNone}
}

// isFree recognises the two spellings vendors use for a no-cost tier:
// openrouter's ":free" suffix and a plain "-free" on the model name.
func isFree(model string) bool {
	m := strings.ToLower(model)
	return strings.HasSuffix(m, ":free") || strings.HasSuffix(m, "-free")
}

func familyTier(model string) (Tier, bool) {
	n := normalise(model)
	for _, t := range deepTokens {
		if strings.Contains(n, t) {
			return TierDeep, true
		}
	}
	for _, t := range lookupTokens {
		if strings.Contains(n, t) {
			return TierLookup, true
		}
	}
	return TierUnknown, false
}

// normalise reduces a model name to the form shared across providers:
// lower case, dots as dashes, no trailing snapshot date.
func normalise(model string) string {
	m := strings.ToLower(model)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	m = strings.ReplaceAll(m, ".", "-")
	if i := strings.LastIndex(m, "-"); i > 0 && len(m)-i == 9 {
		if _, err := parseDate(m[i+1:]); err == nil {
			m = m[:i]
		}
	}
	return m
}

func parseDate(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNotDate
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

type dateErr struct{}

func (dateErr) Error() string { return "not a date" }

var errNotDate = dateErr{}

// tierForCost places a cost among the distinct prices the provider
// charges. Rank rather than absolute price, because "expensive" only
// means anything next to the same vendor's other models — $10/MTok is
// the top of one catalogue and the middle of another.
//
// Distinct prices, because a vendor that keeps three generations of the
// same model at one price would otherwise weight that price three times
// and drag every band with it. The cheapest is always lookup and the
// dearest always deep; what sits between them splits at the midpoint.
func (c *Catalog) tierForCost(provider eventschema.Provider, cost float64) Tier {
	costs := c.providerCosts(provider)
	switch len(costs) {
	case 0:
		return TierUnknown
	case 1:
		return TierDefault
	}
	rank := sort.SearchFloat64s(costs, cost)
	switch {
	case rank <= 0:
		return TierLookup
	case rank >= len(costs)-1:
		return TierDeep
	case rank <= (len(costs)-1)/2:
		return TierBalanced
	default:
		return TierDefault
	}
}

// providerCosts is the provider's distinct priced costs, ascending.
// Free rows are excluded: a zero would anchor the bottom of every band
// and push real models up a tier they have not earned.
func (c *Catalog) providerCosts(provider eventschema.Provider) []float64 {
	seen := map[float64]struct{}{}
	var out []float64
	for k, r := range c.table.Rates {
		if k.Provider != provider {
			continue
		}
		if !c.ranks(k.Model) {
			continue
		}
		cost := r.InputPerMillion + r.OutputPerMillion
		if cost <= 0 {
			continue
		}
		if _, dup := seen[cost]; dup {
			continue
		}
		seen[cost] = struct{}{}
		out = append(out, cost)
	}
	sort.Float64s(out)
	return out
}

// Target answers the routing question: what should this provider run for
// this tier? It returns the cheapest model on the tier, so the answer is
// the least costly way to buy that capability.
//
// Unlike the router this replaces, a zero price is a reason to prefer a
// model rather than to skip it.
func (c *Catalog) Target(provider eventschema.Provider, tier Tier) (string, bool) {
	if c == nil || tier == TierUnknown {
		return "", false
	}
	var (
		best     string
		bestCost = -1.0
	)
	for k, r := range c.table.Rates {
		if k.Provider != provider || !c.ranks(k.Model) {
			continue
		}
		cost := r.InputPerMillion + r.OutputPerMillion
		if c.tierForCost(provider, cost) != tier {
			continue
		}
		// Strip the card's prefix marker: the caller needs a model name
		// it can actually send, not the pattern the rate was filed under.
		name := strings.TrimSuffix(k.Model, "*")
		if bestCost < 0 || cost < bestCost || (cost == bestCost && name < best) {
			best, bestCost = name, cost
		}
	}
	return best, best != ""
}
