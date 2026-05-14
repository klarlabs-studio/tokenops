// Package spend computes monetary cost for LLM requests. Pricing is held
// in a per-provider, per-model table; the engine looks up the rate for an
// observed PromptEvent and emits a USD figure (or a configurable currency).
//
// The package is intentionally a pure function over the event schema:
// callers (proxy-events, analytics aggregator) decide when to recompute,
// and the engine never touches storage. This keeps the data path simple —
// the proxy stamps a live cost on every envelope, and the analytics layer
// can reprice historical events with an updated table when rates change.
package spend

import (
	"errors"
	"fmt"
	"strings"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Rate captures the per-million-token price of a single model. Values are
// in the price table's Currency.
type Rate struct {
	// InputPerMillion is the price of one million input (prompt) tokens.
	InputPerMillion float64
	// OutputPerMillion is the price of one million output tokens.
	OutputPerMillion float64
	// CachedInputPerMillion is the price of cached input tokens. When zero,
	// callers must fall back to InputPerMillion.
	CachedInputPerMillion float64
}

// Effective returns r with zero values backfilled from a default Rate so
// partial overrides (e.g. a YAML override that only sets the output price)
// remain usable.
func (r Rate) Effective(def Rate) Rate {
	if r.InputPerMillion == 0 {
		r.InputPerMillion = def.InputPerMillion
	}
	if r.OutputPerMillion == 0 {
		r.OutputPerMillion = def.OutputPerMillion
	}
	if r.CachedInputPerMillion == 0 {
		r.CachedInputPerMillion = def.CachedInputPerMillion
	}
	return r
}

// Table is the lookup index from (provider, model) to Rate. Models are
// matched by exact ID first, then by registered prefix (e.g. "gpt-4o-"),
// so version-suffixed models inherit their family's rate without needing
// an explicit row per snapshot.
type Table struct {
	// Currency is the ISO 4217 code rates are denominated in (default USD).
	Currency string
	// Rates maps (provider, modelOrPrefix) → Rate. Lookup uses the raw key
	// for exact matches and the same key with a trailing "*" for prefix
	// matches, e.g. "gpt-4o-*".
	Rates map[Key]Rate
}

// Key uniquely identifies a row in Table.Rates.
type Key struct {
	Provider eventschema.Provider
	Model    string
}

// ErrUnknownModel is returned by Lookup when no rate matches the request.
// Callers use errors.Is to detect this condition and decide whether to
// charge zero, log a warning, or refuse to record cost.
var ErrUnknownModel = errors.New("spend: no rate for provider/model")

// ErrNoModelsForProvider is returned by Cheapest when the table has no
// rows for the requested provider. Callers (coaching router etc.) use
// this to fall back to a local backend or disable LLM features.
var ErrNoModelsForProvider = errors.New("spend: no models registered for provider")

// Cheapest returns the lowest-cost model identifier for provider, using
// blended input+output rate as the cost metric. Prefix rows ("gpt-4o-*")
// are returned with the literal "*" suffix because callers usually want
// a concrete model name; the caller is expected to resolve "*" to a real
// snapshot they support (or trim it for downstream APIs that accept the
// family name verbatim — Anthropic does).
//
// Rationale for blended (input + output) cost: real coaching workloads
// generate small output relative to input, so a model with a cheap input
// rate but expensive output rate often still wins. This metric tracks
// that. Future enhancements (token-weighted choice, latency-aware) can
// extend without breaking the signature.
func (t Table) Cheapest(provider eventschema.Provider) (string, Rate, error) {
	var (
		bestModel string
		bestRate  Rate
		bestCost  = -1.0
	)
	for k, r := range t.Rates {
		if k.Provider != provider {
			continue
		}
		cost := r.InputPerMillion + r.OutputPerMillion
		if bestCost < 0 || cost < bestCost {
			bestCost = cost
			bestModel = k.Model
			bestRate = r
		}
	}
	if bestCost < 0 {
		return "", Rate{}, fmt.Errorf("%w: provider=%s", ErrNoModelsForProvider, provider)
	}
	return bestModel, bestRate, nil
}

// DefaultTable returns a fresh Table seeded with public list prices for
// the most common models served by OpenAI, Anthropic, and Google Gemini.
// Prices are USD per million tokens. The values are intentionally
// conservative: callers running on negotiated rates should override via
// MergeOverrides. Update by appending or replacing entries; callers must
// not mutate the returned map directly because it owns process state.
func DefaultTable() Table {
	return Table{
		Currency: "USD",
		Rates: map[Key]Rate{
			// OpenAI — published list prices.
			{eventschema.ProviderOpenAI, "gpt-4o-mini*"}: {
				InputPerMillion: 0.15, OutputPerMillion: 0.60, CachedInputPerMillion: 0.075,
			},
			{eventschema.ProviderOpenAI, "gpt-4o*"}: {
				InputPerMillion: 2.50, OutputPerMillion: 10.00, CachedInputPerMillion: 1.25,
			},
			{eventschema.ProviderOpenAI, "gpt-4-turbo*"}: {
				InputPerMillion: 10.00, OutputPerMillion: 30.00,
			},
			{eventschema.ProviderOpenAI, "gpt-3.5-turbo*"}: {
				InputPerMillion: 0.50, OutputPerMillion: 1.50,
			},
			{eventschema.ProviderOpenAI, "o1*"}: {
				InputPerMillion: 15.00, OutputPerMillion: 60.00,
			},

			// Anthropic — published list prices.
			{eventschema.ProviderAnthropic, "claude-opus-4-7*"}: {
				InputPerMillion: 15.00, OutputPerMillion: 75.00, CachedInputPerMillion: 1.50,
			},
			{eventschema.ProviderAnthropic, "claude-sonnet-4-6*"}: {
				InputPerMillion: 3.00, OutputPerMillion: 15.00, CachedInputPerMillion: 0.30,
			},
			{eventschema.ProviderAnthropic, "claude-haiku-4-5*"}: {
				InputPerMillion: 1.00, OutputPerMillion: 5.00, CachedInputPerMillion: 0.10,
			},
			{eventschema.ProviderAnthropic, "claude-3-5-sonnet*"}: {
				InputPerMillion: 3.00, OutputPerMillion: 15.00, CachedInputPerMillion: 0.30,
			},
			{eventschema.ProviderAnthropic, "claude-3-5-haiku*"}: {
				InputPerMillion: 0.80, OutputPerMillion: 4.00, CachedInputPerMillion: 0.08,
			},

			// Mistral — published list prices for the large + medium
			// families. Le Chat Pro routes through these on the
			// consumer subscription; cost recompute attaches per-token
			// price when the plan window is exceeded and metered
			// billing kicks in.
			{eventschema.ProviderMistral, "mistral-large*"}: {
				InputPerMillion: 2.00, OutputPerMillion: 6.00,
			},
			{eventschema.ProviderMistral, "mistral-medium*"}: {
				InputPerMillion: 0.40, OutputPerMillion: 2.00,
			},
			{eventschema.ProviderMistral, "mistral-small*"}: {
				InputPerMillion: 0.20, OutputPerMillion: 0.60,
			},
			{eventschema.ProviderMistral, "codestral*"}: {
				InputPerMillion: 0.30, OutputPerMillion: 0.90,
			},

			// Google Gemini — published list prices.
			{eventschema.ProviderGemini, "gemini-2.5-pro*"}: {
				InputPerMillion: 1.25, OutputPerMillion: 10.00, CachedInputPerMillion: 0.31,
			},
			{eventschema.ProviderGemini, "gemini-2.5-flash*"}: {
				InputPerMillion: 0.30, OutputPerMillion: 2.50, CachedInputPerMillion: 0.075,
			},
			{eventschema.ProviderGemini, "gemini-1.5-pro*"}: {
				InputPerMillion: 1.25, OutputPerMillion: 5.00,
			},
			{eventschema.ProviderGemini, "gemini-1.5-flash*"}: {
				InputPerMillion: 0.075, OutputPerMillion: 0.30,
			},
		},
	}
}

// MergeOverrides returns a new Table that layers overrides on top of t.
// An override Rate that leaves a field zero inherits from the matching
// base row (per Rate.Effective), so partial YAML overrides are safe.
func (t Table) MergeOverrides(overrides Table) Table {
	merged := Table{
		Currency: t.Currency,
		Rates:    make(map[Key]Rate, len(t.Rates)+len(overrides.Rates)),
	}
	if overrides.Currency != "" {
		merged.Currency = overrides.Currency
	}
	for k, v := range t.Rates {
		merged.Rates[k] = v
	}
	for k, v := range overrides.Rates {
		if base, ok := merged.Rates[k]; ok {
			merged.Rates[k] = v.Effective(base)
		} else {
			merged.Rates[k] = v
		}
	}
	return merged
}

// Lookup resolves the rate for (provider, model). It first tries an exact
// match, then falls back to the longest-prefix entry whose key ends with
// "*" — e.g. "gpt-4o-2024-08-06" matches "gpt-4o*". Returns ErrUnknownModel
// when nothing matches.
func (t Table) Lookup(provider eventschema.Provider, model string) (Rate, error) {
	if model == "" {
		return Rate{}, fmt.Errorf("%w: empty model", ErrUnknownModel)
	}
	if r, ok := t.Rates[Key{provider, model}]; ok {
		return r, nil
	}
	var (
		bestRate Rate
		bestLen  int
		found    bool
	)
	for k, v := range t.Rates {
		if k.Provider != provider {
			continue
		}
		if !strings.HasSuffix(k.Model, "*") {
			continue
		}
		prefix := strings.TrimSuffix(k.Model, "*")
		if !strings.HasPrefix(model, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			bestLen = len(prefix)
			bestRate = v
			found = true
		}
	}
	if !found {
		return Rate{}, fmt.Errorf("%w: provider=%s model=%s", ErrUnknownModel, provider, model)
	}
	return bestRate, nil
}
