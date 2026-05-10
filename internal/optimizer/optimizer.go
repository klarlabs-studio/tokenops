// Package optimizer hosts the pluggable optimization pipeline. Concrete
// optimizers (prompt compression, semantic dedupe, retrieval pruning,
// context trimming, model routing, cache reuse) implement the Optimizer
// interface and register with a Pipeline; the pipeline runs them in order
// against a Request and returns a slice of OptimizationEvent results.
//
// The pipeline supports three modes from the eventschema:
//   - passive: optimizers run, recommendations are emitted, but the
//     request payload is returned unchanged. Decision is "skipped" (we
//     observed an opportunity but did not apply it).
//   - interactive: the pipeline calls Decider on every Recommendation
//     and applies those it accepts; the rest are emitted with the
//     reported decision (rejected/skipped).
//   - replay: optimizers run against historical material so a session
//     can be replayed against the current optimizer set; the original
//     request is unchanged. Used by the coaching engine.
//
// A latency budget caps total wall-clock time spent across the pipeline.
// When the budget is exceeded the pipeline stops invoking further
// optimizers and emits a synthetic "skipped" event for each unrun stage
// so the dashboard can attribute lost opportunities.
package optimizer

import (
	"context"
	"errors"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Mode is the optimization execution mode (mirrors eventschema.OptimizationMode
// values for serialisation).
type Mode = eventschema.OptimizationMode

// Re-export the canonical modes so callers can refer to them via this
// package without importing eventschema.
const (
	ModePassive     = eventschema.OptimizationModePassive
	ModeInteractive = eventschema.OptimizationModeInteractive
	ModeReplay      = eventschema.OptimizationModeReplay
)

// Request carries the input to the optimizer pipeline. PromptHash is the
// canonicalised hash of the request (links optimizations back to the
// triggering PromptEvent). Provider/Model give optimizers context for
// cost-aware decisions; Body is the raw provider payload that text-based
// optimizers (compression, trimming) inspect.
type Request struct {
	PromptHash   string
	Provider     eventschema.Provider
	Model        string
	Body         []byte
	WorkflowID   string
	AgentID      string
	InputTokens  int64
	OutputTokens int64
	Mode         Mode
	// LatencyBudget caps wall-clock time across the pipeline. Zero
	// disables the cap.
	LatencyBudget time.Duration
}

// Recommendation is what an Optimizer returns to the pipeline. The
// pipeline turns each Recommendation into an OptimizationEvent, attaching
// the actual Decision based on Mode and the Decider callback.
type Recommendation struct {
	Kind                   eventschema.OptimizationType
	EstimatedSavingsTokens int64
	EstimatedSavingsUSD    float64
	QualityScore           float64
	Reason                 string
	// LatencyImpact is the additional wall-clock time the optimizer
	// added (negative if it shortened the request).
	LatencyImpact time.Duration
	// ApplyBody, when non-nil, is the rewritten request body that the
	// proxy should forward upstream. Optimizers that only recommend
	// (passive) leave this nil.
	ApplyBody []byte
}

// Optimizer is the unit of optimization. Run produces zero or more
// Recommendations against the Request. Implementations must be safe for
// concurrent use; the pipeline may invoke Run from multiple goroutines.
type Optimizer interface {
	// Kind reports the optimizer category (one of eventschema.OptimizationType).
	Kind() eventschema.OptimizationType
	// Run analyses req and returns recommendations. Returning nil means
	// the optimizer found no opportunity. Errors abort the optimizer's
	// contribution but do not stop the pipeline.
	Run(ctx context.Context, req *Request) ([]Recommendation, error)
}

// Decider decides whether a recommendation should be applied. The
// pipeline invokes it in interactive mode for every recommendation that
// carries an ApplyBody. Returning false (or any returned error) yields
// OptimizationDecisionRejected. In passive and replay mode the Decider
// is not invoked.
type Decider func(ctx context.Context, rec Recommendation) (bool, error)

// Result aggregates the pipeline's output. Body is the final request
// body the caller should forward upstream — equal to Request.Body in
// passive/replay mode, or the last accepted ApplyBody in interactive
// mode. Events captures the per-recommendation OptimizationEvents.
type Result struct {
	Body         []byte
	Events       []*eventschema.OptimizationEvent
	TotalLatency time.Duration
	BudgetHit    bool
}

// Pipeline owns an ordered list of optimizers. The zero value is unusable;
// construct via NewPipeline.
type Pipeline struct {
	optimizers []Optimizer
	clock      func() time.Time
}

// NewPipeline returns a pipeline running the given optimizers in order.
func NewPipeline(opts ...Optimizer) *Pipeline {
	return &Pipeline{
		optimizers: append([]Optimizer(nil), opts...),
		clock:      time.Now,
	}
}

// SetClock overrides the wall-clock used for budget enforcement (tests).
func (p *Pipeline) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	p.clock = now
}

// Optimizers returns the configured optimizers in order.
func (p *Pipeline) Optimizers() []Optimizer {
	return append([]Optimizer(nil), p.optimizers...)
}

// Run executes the pipeline over req. decider may be nil; it is only
// consulted in interactive mode.
func (p *Pipeline) Run(ctx context.Context, req *Request, decider Decider) (*Result, error) {
	if req == nil {
		return nil, errors.New("optimizer: request must not be nil")
	}
	mode := req.Mode
	if mode == "" {
		mode = ModePassive
	}
	body := req.Body

	res := &Result{Body: body}
	start := p.clock()

	for _, opt := range p.optimizers {
		// Budget check before every optimizer so the next one runs only
		// if there is time. Optimizers that exceed the budget mid-run
		// are still attributed; we record their latency and stop.
		if req.LatencyBudget > 0 && p.clock().Sub(start) >= req.LatencyBudget {
			res.BudgetHit = true
			res.Events = append(res.Events, p.budgetSkippedEvent(req, opt, mode))
			continue
		}

		stageStart := p.clock()
		recs, err := opt.Run(ctx, req)
		stageElapsed := p.clock().Sub(stageStart)

		if err != nil {
			res.Events = append(res.Events, p.errorEvent(req, opt, mode, err, stageElapsed))
			continue
		}

		for _, rec := range recs {
			decision, applied := p.decide(ctx, mode, rec, decider)
			if applied && len(rec.ApplyBody) > 0 {
				body = rec.ApplyBody
				res.Body = body
			}
			res.Events = append(res.Events, p.recommendationEvent(req, opt, mode, rec, decision, stageElapsed))
		}
	}

	res.TotalLatency = p.clock().Sub(start)
	return res, nil
}

func (p *Pipeline) decide(ctx context.Context, mode Mode, rec Recommendation, decider Decider) (eventschema.OptimizationDecision, bool) {
	switch mode {
	case ModeInteractive:
		if decider == nil {
			return eventschema.OptimizationDecisionRejected, false
		}
		ok, err := decider(ctx, rec)
		if err != nil || !ok {
			return eventschema.OptimizationDecisionRejected, false
		}
		if len(rec.ApplyBody) == 0 {
			// Accepted but optimizer returned no payload — record as
			// accepted but do not mutate body.
			return eventschema.OptimizationDecisionAccepted, false
		}
		return eventschema.OptimizationDecisionApplied, true
	case ModeReplay:
		// Replay records what would have happened without applying.
		return eventschema.OptimizationDecisionSkipped, false
	default:
		// Passive: observed-only.
		return eventschema.OptimizationDecisionSkipped, false
	}
}

func (p *Pipeline) recommendationEvent(req *Request, opt Optimizer, mode Mode, rec Recommendation, decision eventschema.OptimizationDecision, stageLatency time.Duration) *eventschema.OptimizationEvent {
	kind := rec.Kind
	if kind == "" {
		kind = opt.Kind()
	}
	latencyNs := rec.LatencyImpact.Nanoseconds()
	if latencyNs == 0 {
		latencyNs = stageLatency.Nanoseconds()
	}
	return &eventschema.OptimizationEvent{
		PromptHash:             req.PromptHash,
		Kind:                   kind,
		Mode:                   mode,
		EstimatedSavingsTokens: rec.EstimatedSavingsTokens,
		EstimatedSavingsUSD:    rec.EstimatedSavingsUSD,
		QualityScore:           rec.QualityScore,
		Decision:               decision,
		Reason:                 rec.Reason,
		LatencyImpactNS:        latencyNs,
		WorkflowID:             req.WorkflowID,
		AgentID:                req.AgentID,
	}
}

func (p *Pipeline) errorEvent(req *Request, opt Optimizer, mode Mode, err error, stageLatency time.Duration) *eventschema.OptimizationEvent {
	return &eventschema.OptimizationEvent{
		PromptHash:      req.PromptHash,
		Kind:            opt.Kind(),
		Mode:            mode,
		Decision:        eventschema.OptimizationDecisionSkipped,
		Reason:          "optimizer_error: " + err.Error(),
		LatencyImpactNS: stageLatency.Nanoseconds(),
		WorkflowID:      req.WorkflowID,
		AgentID:         req.AgentID,
	}
}

func (p *Pipeline) budgetSkippedEvent(req *Request, opt Optimizer, mode Mode) *eventschema.OptimizationEvent {
	return &eventschema.OptimizationEvent{
		PromptHash: req.PromptHash,
		Kind:       opt.Kind(),
		Mode:       mode,
		Decision:   eventschema.OptimizationDecisionSkipped,
		Reason:     "latency_budget_exceeded",
		WorkflowID: req.WorkflowID,
		AgentID:    req.AgentID,
	}
}
