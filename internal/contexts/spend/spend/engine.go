package spend

import (
	"sort"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// DatedTable pairs a pricing Table with the instant from which it takes
// effect. It is the effective-dating primitive: an Engine built from a
// series of DatedTables prices each event at the rate card that was in
// force at the event's timestamp (ADR 0002 Phase 2).
type DatedTable struct {
	// EffectiveFrom is the UTC instant this table becomes authoritative.
	// A table is selected for an event whose Timestamp is >= EffectiveFrom
	// and < the next table's EffectiveFrom. The zero time matches every
	// event, which is how a single-table Engine (NewEngine) prices
	// timestamp-independently.
	EffectiveFrom time.Time
	// Table is the rate card in effect from EffectiveFrom onward.
	Table Table
}

// Engine computes monetary cost from observed PromptEvents. Internally it
// holds one or more effective-dated Tables sorted ascending by
// EffectiveFrom; each Compute selects the table in force at the event's
// timestamp. It is safe for concurrent use after construction; the tables
// are treated as immutable.
type Engine struct {
	// tables is sorted ascending by EffectiveFrom and always has len >= 1.
	tables []DatedTable
}

// NewEngine builds an Engine over a single Table effective from the zero
// time, so it matches every event regardless of timestamp. Pass
// DefaultTable() (optionally merged with overrides) for production usage.
// Backward-compatible: a single-table Engine prices identically to the
// pre-effective-dating engine.
func NewEngine(t Table) *Engine {
	return NewDatedEngine([]DatedTable{{EffectiveFrom: time.Time{}, Table: t}})
}

// NewDatedEngine builds an Engine over a series of effective-dated tables.
// The tables are stored sorted ascending by EffectiveFrom. Compute selects
// the table with the greatest EffectiveFrom <= the event's Timestamp; an
// event that predates every table (or carries a zero Timestamp) uses the
// EARLIEST table, so pricing never fails for lack of a dated table.
//
// An empty tables slice yields an engine over a single empty table (every
// Lookup returns ErrUnknownModel) rather than a nil-deref hazard.
func NewDatedEngine(tables []DatedTable) *Engine {
	if len(tables) == 0 {
		tables = []DatedTable{{}}
	}
	normalized := make([]DatedTable, len(tables))
	copy(normalized, tables)
	for i := range normalized {
		if normalized[i].Table.Rates == nil {
			normalized[i].Table.Rates = map[Key]Rate{}
		}
		if normalized[i].Table.Currency == "" {
			normalized[i].Table.Currency = "USD"
		}
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].EffectiveFrom.Before(normalized[j].EffectiveFrom)
	})
	return &Engine{tables: normalized}
}

// tableFor returns the rate card in effect at ts: the table with the
// greatest EffectiveFrom <= ts. When ts predates every table (including the
// zero time), it returns the earliest table so pricing never fails for lack
// of a dated table. The selected table is authoritative for that instant —
// a missing (provider, model) is NOT resolved against a different-dated
// table.
func (e *Engine) tableFor(ts time.Time) Table {
	selected := e.tables[0].Table // earliest, the never-fail default
	for _, dt := range e.tables {
		if dt.EffectiveFrom.After(ts) {
			break
		}
		selected = dt.Table
	}
	return selected
}

// Currency returns the ISO 4217 code costs are denominated in, taken from
// the latest-effective table.
func (e *Engine) Currency() string { return e.tables[len(e.tables)-1].Table.Currency }

// Table returns the LATEST-effective pricing table — the "current rates"
// most callers want. Historical repricing goes through Compute, which
// selects per-event. Callers must not mutate the returned table.
func (e *Engine) Table() Table { return e.tables[len(e.tables)-1].Table }

// Compute returns the monetary cost of a PromptEvent. Cached input tokens
// are billed at the cached rate when set; the remaining input tokens use
// the regular input rate. Returns ErrUnknownModel when the event's
// (provider, model) is not in the table — callers commonly fall back to
// zero cost and emit a structured warning.
//
// Plan-included and trial events short-circuit to zero cost: the
// request is covered by a flat-rate subscription or vendor-issued
// credit, so per-token billing does not apply. The token counts still
// flow through the analytics pipeline so plan quota / headroom math
// (internal/contexts/spend/plans) sees them.
func (e *Engine) Compute(p *eventschema.PromptEvent) (float64, error) {
	return e.ComputeAt(p, time.Time{})
}

// ComputeAt is Compute priced at the rate card in effect at the instant at
// (ADR 0002 Phase 2 effective dating). The PromptEvent itself carries no
// occurrence time — it lives on the enclosing Envelope — so callers that
// know the event's timestamp (the analytics repricing pipeline, the proxy
// envelope path) pass it here to price historical events at the rate that
// was in force then. A zero at (or an event predating every dated table)
// prices at the earliest table, so costing never fails for lack of a dated
// table. For a single-table Engine (NewEngine) at is irrelevant: every
// instant selects the one table, so ComputeAt == Compute.
func (e *Engine) ComputeAt(p *eventschema.PromptEvent, at time.Time) (float64, error) {
	if p == nil {
		return 0, ErrUnknownModel
	}
	switch p.CostSource {
	case eventschema.CostSourcePlanIncluded, eventschema.CostSourceTrial:
		return 0, nil
	}
	model := p.RequestModel
	if p.ResponseModel != "" {
		// Prefer the model the provider actually billed against.
		model = p.ResponseModel
	}
	// Price at the rate card that was in effect at the event's timestamp.
	rate, err := e.tableFor(at).Lookup(p.Provider, model)
	if err != nil {
		return 0, err
	}
	cached := p.CachedInputTokens
	if cached < 0 {
		cached = 0
	}
	if cached > p.InputTokens {
		cached = p.InputTokens
	}
	uncached := p.InputTokens - cached

	cachedRate := rate.CachedInputPerMillion
	if cachedRate == 0 {
		cachedRate = rate.InputPerMillion
	}

	cost := perMillion(uncached, rate.InputPerMillion) +
		perMillion(cached, cachedRate) +
		perMillion(p.OutputTokens, rate.OutputPerMillion)
	return cost, nil
}

// Apply stamps Compute's result onto p.CostUSD. Errors leave the value
// untouched so a missing-rate entry does not zero out a previously
// computed cost.
func (e *Engine) Apply(p *eventschema.PromptEvent) error {
	return e.ApplyAt(p, time.Time{})
}

// ApplyAt is Apply priced at the rate card in effect at at (see ComputeAt).
func (e *Engine) ApplyAt(p *eventschema.PromptEvent, at time.Time) error {
	cost, err := e.ComputeAt(p, at)
	if err != nil {
		return err
	}
	p.CostUSD = cost
	return nil
}

// ApplyToEnvelope is a convenience that calls ApplyAt when env carries a
// PromptEvent payload and is a no-op otherwise. The envelope's Timestamp is
// the event's occurrence time, so the cost is stamped at the rate card in
// effect then (effective dating). Workflow / Optimization / Coaching events
// do not carry direct token counts wired to a model, so they are left to
// the analytics aggregator to roll up.
func (e *Engine) ApplyToEnvelope(env *eventschema.Envelope) error {
	if env == nil {
		return nil
	}
	p, ok := env.Payload.(*eventschema.PromptEvent)
	if !ok {
		return nil
	}
	return e.ApplyAt(p, env.Timestamp)
}

// perMillion is a small helper that turns a token count and a $/M rate
// into a USD figure. Kept private because the math is trivial but the
// accidental swap of factors would silently mis-cost everything.
func perMillion(tokens int64, ratePerMillion float64) float64 {
	if tokens <= 0 || ratePerMillion <= 0 {
		return 0
	}
	return float64(tokens) * ratePerMillion / 1_000_000.0
}
