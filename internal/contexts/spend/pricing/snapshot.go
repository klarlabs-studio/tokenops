// Package pricing implements the researched, sourced, effective-dated
// pricing-snapshot framework from ADR 0002. It turns the model rate card
// from a single hand-maintained table into a series of timestamped,
// provenance-carrying snapshots fetched from a pluggable Source.
//
// Phase 1 (this package) builds the framework: a Snapshot model, an
// append-only on-disk Store, a pluggable Source (default: LiteLLM), a
// consistency Guard (the exact check that caught the Opus ⅓ error), and a
// Diff so a `pricing refresh` shouts drift instead of hiding it. The cost
// engine is intentionally NOT wired to snapshots yet — it keeps using
// spend.DefaultTable(); effective-dated selection is Phase 2.
//
// Everything here is nil-safe and fail-soft: an absent snapshot dir, an
// unreachable source, or a malformed file degrades to the embedded baseline
// rather than erroring the caller. Snapshots cover every provider the catalog
// prices; each rate is keyed "<provider>/<model>" so the key-space matches the
// multi-provider engine table. The consistency guard's ratio heuristics
// (cache-read ≈ 10% of input, output ≈ 5× input) remain an Anthropic-family
// invariant and run only on anthropic/* rows (see guard.go).
package pricing

import (
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// snapKeySep separates the provider from the model in a Snapshot rate key.
const snapKeySep = "/"

// snapKey builds the "<provider>/<model>" key a Snapshot rate is stored under,
// e.g. snapKey("anthropic", "claude-opus-4-8") == "anthropic/claude-opus-4-8".
// Keeping the provider in the string key preserves the clean JSON on-disk
// format while letting the snapshot span every provider the catalog prices.
func snapKey(provider, model string) string {
	return provider + snapKeySep + model
}

// splitSnapKey splits a Snapshot rate key back into its provider and model.
// A key with no separator is treated as a bare model with an empty provider
// (fail-soft: legacy or hand-written keys degrade rather than panic).
func splitSnapKey(key string) (provider, model string) {
	if p, m, ok := strings.Cut(key, snapKeySep); ok {
		return p, m
	}
	return "", key
}

// baselineFetchedAt is the fixed, committed timestamp of the embedded
// baseline snapshot. It is dated at the ADR's acceptance so the baseline
// sorts before any real refresh and stays deterministic across machines
// (a moving timestamp would make BaselineSnapshot non-reproducible).
var baselineFetchedAt = time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)

// Rate captures the per-million-token price of a single model. It mirrors
// spend.Rate but carries stable JSON tags so the on-disk snapshot format is
// decoupled from the engine's internal type. Convert with FromSpendRate /
// ToSpendRate.
type Rate struct {
	InputPerMillion       float64 `json:"input_per_million"`
	OutputPerMillion      float64 `json:"output_per_million"`
	CachedInputPerMillion float64 `json:"cached_input_per_million"`
}

// FromSpendRate adapts an engine rate into the snapshot rate type.
func FromSpendRate(r spend.Rate) Rate {
	return Rate{
		InputPerMillion:       r.InputPerMillion,
		OutputPerMillion:      r.OutputPerMillion,
		CachedInputPerMillion: r.CachedInputPerMillion,
	}
}

// ToSpendRate adapts a snapshot rate back into the engine rate type.
func (r Rate) ToSpendRate() spend.Rate {
	return spend.Rate{
		InputPerMillion:       r.InputPerMillion,
		OutputPerMillion:      r.OutputPerMillion,
		CachedInputPerMillion: r.CachedInputPerMillion,
	}
}

// Snapshot is a point-in-time rate card with provenance. Rates is keyed by
// "<provider>/<model>" (e.g. "anthropic/claude-opus-4-8", "openai/gpt-4o"),
// where model is the tokenops catalog key with the trailing "*" prefix marker
// stripped. Use snapKey / splitSnapKey to build and parse keys.
type Snapshot struct {
	Source    string          `json:"source"`
	SourceURL string          `json:"source_url,omitempty"`
	FetchedAt time.Time       `json:"fetched_at"`
	Rates     map[string]Rate `json:"rates"`
}

// SourceEmbeddedBaseline is the Source value of BaselineSnapshot.
const SourceEmbeddedBaseline = "embedded-baseline"

// BaselineSnapshot wraps the embedded pricing.yaml catalog as the always-
// present fallback snapshot. It carries a fixed FetchedAt so it is
// deterministic and sorts before any refresh. Every provider/model the catalog
// prices is included, keyed "<provider>/<model>" with the trailing "*" prefix
// marker stripped so keys line up with a fetched snapshot for diffing.
func BaselineSnapshot() Snapshot {
	table := spend.DefaultTable()
	rates := make(map[string]Rate, len(table.Rates))
	for k, r := range table.Rates {
		rates[snapKey(string(k.Provider), normalizeKey(k.Model))] = FromSpendRate(r)
	}
	return Snapshot{
		Source:    SourceEmbeddedBaseline,
		FetchedAt: baselineFetchedAt,
		Rates:     rates,
	}
}

// normalizeKey strips the catalog's trailing "*" prefix marker and any
// surrounding whitespace, yielding a plain model key.
func normalizeKey(model string) string {
	return strings.TrimSpace(strings.TrimSuffix(model, "*"))
}

// Table builds a spend.Table from the snapshot's rates across every provider.
// Each "<provider>/<model>" key becomes a spend.Key{Provider, Model+"*"}: the
// trailing "*" re-adds the catalog's prefix-match marker, so version-suffixed
// request models (e.g. "claude-opus-4-8[1m]") resolve to their family rate.
// The table is multi-provider but may not be complete; callers that need a
// full rate card layer it onto spend.DefaultTable (see SnapshotsToDatedTables).
func (s Snapshot) Table() spend.Table {
	rates := make(map[spend.Key]spend.Rate, len(s.Rates))
	for key, r := range s.Rates {
		provider, model := splitSnapKey(key)
		k := spend.Key{Provider: eventschema.Provider(provider), Model: normalizeKey(model) + "*"}
		rates[k] = r.ToSpendRate()
	}
	return spend.Table{Currency: "USD", Rates: rates}
}

// Models returns the snapshot's "<provider>/<model>" keys sorted lexically,
// which groups rows by provider, for stable display and iteration.
func (s Snapshot) Models() []string {
	out := make([]string, 0, len(s.Rates))
	for k := range s.Rates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
