// Package replay reloads historical sessions from the local SQLite event
// store and runs the optimizer pipeline against them in replay mode. The
// goal is offline introspection — "what would my last week of agent runs
// have cost with the current optimizer set?" — without ever touching the
// upstream provider.
//
// The engine is a pure read+compute layer. It never writes back to the
// store; replay events are returned to the caller (CLI / coaching engine)
// which decides whether to persist them as a separate audit record.
package replay

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// SessionSelector identifies the slice of history to replay. Exactly one
// of WorkflowID / SessionID / AgentID should be set; combining them
// AND-restricts. Since/Until further narrow the window.
type SessionSelector struct {
	WorkflowID string
	SessionID  string
	AgentID    string
	Since      time.Time
	Until      time.Time
	Limit      int
}

// StepDiff is one prompt's before/after contrast.
type StepDiff struct {
	OriginalEnvelope     *eventschema.Envelope
	OriginalCostUSD      float64
	OriginalInputTokens  int64
	OriginalOutputTokens int64
	OptimizationEvents   []*eventschema.OptimizationEvent
	// EstimatedSavingsTokens is the sum of estimated savings across the
	// step's optimization recommendations.
	EstimatedSavingsTokens int64
	// EstimatedSavingsUSD is the spend savings recomputed via spend.Engine
	// using the recommendation's EstimatedSavingsTokens applied at the
	// step's input rate.
	EstimatedSavingsUSD float64
}

// Result aggregates a session replay.
type Result struct {
	Steps                  []StepDiff
	OriginalCostUSD        float64
	EstimatedSavingsTokens int64
	EstimatedSavingsUSD    float64
	OriginalInputTokens    int64
	OriginalOutputTokens   int64
}

// SavingsRatio returns EstimatedSavingsUSD / OriginalCostUSD or 0 when
// the original cost was zero.
func (r Result) SavingsRatio() float64 {
	if r.OriginalCostUSD == 0 {
		return 0
	}
	return r.EstimatedSavingsUSD / r.OriginalCostUSD
}

// Engine replays sessions through an optimizer pipeline.
type Engine struct {
	store    *sqlite.Store
	pipeline *optimizer.Pipeline
	spend    *spend.Engine
}

// New constructs an Engine. The pipeline is run in replay mode, so its
// optimizers do not mutate request bodies — they only emit
// recommendations. spendEng may be nil; savings then surface as token
// counts only.
func New(store *sqlite.Store, pipeline *optimizer.Pipeline, spendEng *spend.Engine) *Engine {
	return &Engine{store: store, pipeline: pipeline, spend: spendEng}
}

// Replay loads the session selected by sel and runs the optimizer
// pipeline against every PromptEvent. Returns ErrEmptySession when no
// matching prompts are found so callers can produce a friendly message.
func (e *Engine) Replay(ctx context.Context, sel SessionSelector) (*Result, error) {
	if e == nil || e.store == nil {
		return nil, errors.New("replay: engine not initialised")
	}
	if e.pipeline == nil {
		return nil, errors.New("replay: pipeline not configured")
	}
	envs, err := e.loadSession(ctx, sel)
	if err != nil {
		return nil, err
	}
	if len(envs) == 0 {
		return nil, ErrEmptySession
	}

	res := &Result{}
	for _, env := range envs {
		pe, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok {
			continue
		}
		step, err := e.replayStep(ctx, env, pe)
		if err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, step)
		res.OriginalCostUSD += step.OriginalCostUSD
		res.OriginalInputTokens += step.OriginalInputTokens
		res.OriginalOutputTokens += step.OriginalOutputTokens
		res.EstimatedSavingsTokens += step.EstimatedSavingsTokens
		res.EstimatedSavingsUSD += step.EstimatedSavingsUSD
	}
	return res, nil
}

// ErrEmptySession is returned by Replay when the selector matched no
// prompt events.
var ErrEmptySession = errors.New("replay: empty session")

func (e *Engine) loadSession(ctx context.Context, sel SessionSelector) ([]*eventschema.Envelope, error) {
	limit := sel.Limit
	if limit <= 0 {
		limit = 1000
	}
	envs, err := e.store.Query(ctx, sqlite.Filter{
		Type:       eventschema.EventTypePrompt,
		WorkflowID: sel.WorkflowID,
		SessionID:  sel.SessionID,
		AgentID:    sel.AgentID,
		Since:      sel.Since,
		Until:      sel.Until,
		Limit:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("replay: load session: %w", err)
	}
	return envs, nil
}

func (e *Engine) replayStep(ctx context.Context, env *eventschema.Envelope, pe *eventschema.PromptEvent) (StepDiff, error) {
	step := StepDiff{
		OriginalEnvelope:     env,
		OriginalCostUSD:      pe.CostUSD,
		OriginalInputTokens:  pe.InputTokens,
		OriginalOutputTokens: pe.OutputTokens,
	}
	if step.OriginalCostUSD == 0 && e.spend != nil {
		if c, err := e.spend.Compute(pe); err == nil {
			step.OriginalCostUSD = c
		}
	}

	req := &optimizer.Request{
		PromptHash:   pe.PromptHash,
		Provider:     pe.Provider,
		Model:        pe.RequestModel,
		WorkflowID:   pe.WorkflowID,
		AgentID:      pe.AgentID,
		InputTokens:  pe.InputTokens,
		OutputTokens: pe.OutputTokens,
		Mode:         optimizer.ModeReplay,
	}
	out, err := e.pipeline.Run(ctx, req, nil)
	if err != nil {
		return step, fmt.Errorf("replay: pipeline: %w", err)
	}
	step.OptimizationEvents = out.Events

	for _, ev := range out.Events {
		step.EstimatedSavingsTokens += ev.EstimatedSavingsTokens
	}

	// Spend savings: when the recommendation already carries an USD
	// estimate, sum it; otherwise translate token savings via spend.Engine
	// using the step's input rate.
	for _, ev := range out.Events {
		if ev.EstimatedSavingsUSD > 0 {
			step.EstimatedSavingsUSD += ev.EstimatedSavingsUSD
			continue
		}
		if e.spend == nil || ev.EstimatedSavingsTokens == 0 {
			continue
		}
		// Approximate: assume saved tokens are input-side. This is the
		// dominant case for compression / dedupe / context-trim; output-
		// side savings (e.g. routing) carry their own USD estimate.
		fake := &eventschema.PromptEvent{
			Provider: pe.Provider, RequestModel: pe.RequestModel,
			InputTokens: ev.EstimatedSavingsTokens,
		}
		if c, err := e.spend.Compute(fake); err == nil {
			step.EstimatedSavingsUSD += c
		}
	}
	return step, nil
}
