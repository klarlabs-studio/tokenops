package spend

import (
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Engine computes monetary cost from observed PromptEvents using a Table.
// It is safe for concurrent use after construction; the Table is treated
// as immutable.
type Engine struct {
	table Table
}

// NewEngine builds an Engine over t. Pass DefaultTable() (optionally
// merged with overrides) for production usage.
func NewEngine(t Table) *Engine {
	if t.Rates == nil {
		t.Rates = map[Key]Rate{}
	}
	if t.Currency == "" {
		t.Currency = "USD"
	}
	return &Engine{table: t}
}

// Currency returns the ISO 4217 code costs are denominated in.
func (e *Engine) Currency() string { return e.table.Currency }

// Table returns the underlying pricing table. Callers must not mutate it.
func (e *Engine) Table() Table { return e.table }

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
	rate, err := e.table.Lookup(p.Provider, model)
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
	cost, err := e.Compute(p)
	if err != nil {
		return err
	}
	p.CostUSD = cost
	return nil
}

// ApplyToEnvelope is a convenience that calls Apply when env carries a
// PromptEvent payload and is a no-op otherwise. Workflow / Optimization /
// Coaching events do not carry direct token counts wired to a model, so
// they are left to the analytics aggregator to roll up.
func (e *Engine) ApplyToEnvelope(env *eventschema.Envelope) error {
	if env == nil {
		return nil
	}
	p, ok := env.Payload.(*eventschema.PromptEvent)
	if !ok {
		return nil
	}
	return e.Apply(p)
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
