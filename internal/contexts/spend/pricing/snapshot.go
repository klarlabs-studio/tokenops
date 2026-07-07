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
// rather than erroring the caller. Snapshots are Anthropic-scoped because the
// consistency heuristics (cache-read ≈ 10% of input, output ≈ 5× input) are a
// per-family invariant and the drift the ADR targets was an Anthropic row.
package pricing

import (
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SnapshotProvider is the vendor family a Snapshot prices. Phase 1 scopes
// snapshots to Anthropic: the consistency guard's ratios are a per-family
// invariant and the ADR's motivating drift (Opus at ⅓) was Anthropic.
const SnapshotProvider = eventschema.ProviderAnthropic

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
// tokenops model key (e.g. "claude-opus-4-8"), the same keys the embedded
// catalog uses with the trailing "*" stripped.
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
// deterministic and sorts before any refresh. Only the SnapshotProvider
// family is included, and the trailing "*" prefix marker is stripped from
// keys so they line up with a fetched snapshot for diffing.
func BaselineSnapshot() Snapshot {
	table := spend.DefaultTable()
	rates := make(map[string]Rate, len(table.Rates))
	for k, r := range table.Rates {
		if k.Provider != SnapshotProvider {
			continue
		}
		rates[normalizeKey(k.Model)] = FromSpendRate(r)
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

// Table builds a spend.Table from the snapshot's rates, scoped to the
// SnapshotProvider family. Snapshot keys are normalized (the catalog's
// trailing "*" prefix marker is stripped), so Table re-adds it: every row
// becomes a prefix match, exactly as the embedded catalog stores Anthropic
// models. That keeps version-suffixed request models (e.g.
// "claude-opus-4-8[1m]") resolving to their family rate. The table is
// Anthropic-scoped only — callers that need a complete rate card layer it
// onto spend.DefaultTable (see SnapshotsToDatedTables).
func (s Snapshot) Table() spend.Table {
	rates := make(map[spend.Key]spend.Rate, len(s.Rates))
	for model, r := range s.Rates {
		key := spend.Key{Provider: SnapshotProvider, Model: normalizeKey(model) + "*"}
		rates[key] = r.ToSpendRate()
	}
	return spend.Table{Currency: "USD", Rates: rates}
}

// Models returns the snapshot's model keys sorted lexically, for stable
// display and iteration.
func (s Snapshot) Models() []string {
	out := make([]string, 0, len(s.Rates))
	for k := range s.Rates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
