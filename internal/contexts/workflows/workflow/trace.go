// Package workflow reconstructs multi-step agent runs from the local
// PromptEvent stream. A "workflow" is the bag of prompts that share a
// workflow_id; the package orders them by timestamp, computes per-step
// deltas (context growth, latency), and rolls them up into a Trace the
// dashboard / coaching engine consume.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Step is one prompt within a workflow trace. ContextDelta is the
// change in InputTokens vs the previous step (positive = context grew).
type Step struct {
	Index        int
	Envelope     *eventschema.Envelope
	Prompt       *eventschema.PromptEvent
	ContextDelta int64
	Latency      time.Duration
	StartGap     time.Duration
	// CostUSD is what the step cost: the stored figure, or the rate
	// card's. Zero when a plan covered it.
	CostUSD float64
	// ListCostUSD is what the step would cost at API rates, including when
	// a plan covered it. On a flat plan it is the measure of how much of
	// the plan's allowance the step used, not a charge.
	ListCostUSD float64
}

// Trace aggregates the workflow.
type Trace struct {
	WorkflowID         string
	Steps              []Step
	StartedAt          time.Time
	EndedAt            time.Time
	Duration           time.Duration
	TotalInputTokens   int64
	TotalOutputTokens  int64
	TotalTotalTokens   int64
	TotalCostUSD       float64
	StepCount          int
	MaxContextSize     int64
	ContextGrowthTotal int64 // sum of positive ContextDeltas
	Models             map[string]int
	Agents             map[string]int
}

// ErrNoTrace is returned by Reconstruct when no prompts match the
// workflow id.
var ErrNoTrace = errors.New("workflow: no prompts found for workflow_id")

// Reconstruct loads all prompt events for workflowID, orders them, and
// computes step deltas + rollups. spendEng may be nil — costs then come
// purely from the stored CostUSD field.
// eventPublisher is optional; adapters wire it via SetEventBus so this
// package remains usable without a canonical event bus in unit tests.
var eventPublisher DomainEventPublisher

// DomainEventPublisher is the narrow canonical-envelope publication port.
type DomainEventPublisher interface {
	Publish(*eventschema.Envelope)
}

// SetEventBus installs the canonical envelope event bus. nil clears it.
// Called once from the daemon composition root.
func SetEventBus(b DomainEventPublisher) { eventPublisher = b }

func Reconstruct(ctx context.Context, store *sqlite.Store, spendEng *spend.Engine, workflowID string) (*Trace, error) {
	if store == nil {
		return nil, errors.New("workflow: store is nil")
	}
	if workflowID == "" {
		return nil, errors.New("workflow: workflowID required")
	}

	envs, err := store.Query(ctx, sqlite.Filter{
		Type:       eventschema.EventTypePrompt,
		WorkflowID: workflowID,
		Limit:      10_000,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow: query: %w", err)
	}
	if len(envs) == 0 {
		return nil, ErrNoTrace
	}

	t := &Trace{
		WorkflowID: workflowID,
		Models:     map[string]int{},
		Agents:     map[string]int{},
		StartedAt:  envs[0].Timestamp,
		EndedAt:    envs[len(envs)-1].Timestamp,
	}
	for i, env := range envs {
		pe, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok {
			continue
		}
		step := Step{
			Index:    i,
			Envelope: env,
			Prompt:   pe,
			Latency:  pe.Latency,
		}
		if i > 0 {
			prev := envs[i-1]
			if prevPE, ok := prev.Payload.(*eventschema.PromptEvent); ok {
				step.ContextDelta = pe.InputTokens - prevPE.InputTokens
			}
			step.StartGap = env.Timestamp.Sub(prev.Timestamp)
		}
		cost := pe.CostUSD
		if cost == 0 && spendEng != nil {
			if c, err := spendEng.Compute(pe); err == nil {
				cost = c
			}
		}
		step.CostUSD = cost
		step.ListCostUSD = listCost(spendEng, pe, cost, env.Timestamp)
		t.Steps = append(t.Steps, step)

		t.TotalInputTokens += pe.InputTokens
		t.TotalOutputTokens += pe.OutputTokens
		t.TotalTotalTokens += pe.TotalTokens
		t.TotalCostUSD += cost

		if pe.InputTokens > t.MaxContextSize {
			t.MaxContextSize = pe.InputTokens
		}
		if step.ContextDelta > 0 {
			t.ContextGrowthTotal += step.ContextDelta
		}
		if pe.RequestModel != "" {
			t.Models[pe.RequestModel]++
		}
		if pe.AgentID != "" {
			t.Agents[pe.AgentID]++
		}
	}
	t.StepCount = len(t.Steps)
	t.Duration = t.EndedAt.Sub(t.StartedAt)

	// Reconstruct is an offline observation, not a live transition —
	// publish WorkflowObserved rather than Started/Completed so
	// subscribers can distinguish replay from real progress.
	if eventPublisher != nil && len(t.Steps) > 0 {
		env, err := eventschema.NewDomainEnvelope("workflow.observed", struct {
			WorkflowID string    `json:"WorkflowID"`
			StepCount  int64     `json:"StepCount"`
			At         time.Time `json:"At"`
		}{workflowID, int64(t.StepCount), t.EndedAt}, t.EndedAt, "workflows", eventschema.Association{})
		if err == nil {
			eventPublisher.Publish(env)
		}
	}
	return t, nil
}

// Summary is a compact rollup suitable for table-style display.
type Summary struct {
	WorkflowID         string
	StepCount          int
	TotalTokens        int64
	TotalCostUSD       float64
	Duration           time.Duration
	MaxContextSize     int64
	ContextGrowthTotal int64
	UniqueModels       int
	UniqueAgents       int
}

// Summarize returns a compact view of the trace.
func (t *Trace) Summarize() Summary {
	if t == nil {
		return Summary{}
	}
	return Summary{
		WorkflowID:         t.WorkflowID,
		StepCount:          t.StepCount,
		TotalTokens:        t.TotalTotalTokens,
		TotalCostUSD:       t.TotalCostUSD,
		Duration:           t.Duration,
		MaxContextSize:     t.MaxContextSize,
		ContextGrowthTotal: t.ContextGrowthTotal,
		UniqueModels:       len(t.Models),
		UniqueAgents:       len(t.Agents),
	}
}

// listCost is a step's cost at API rates. A plan-covered step is priced
// as if it were not; anything else already is.
func listCost(eng *spend.Engine, pe *eventschema.PromptEvent, cost float64, at time.Time) float64 {
	if eng == nil || cost > 0 {
		return cost
	}
	switch pe.CostSource {
	case eventschema.CostSourcePlanIncluded, eventschema.CostSourceTrial:
		unplanned := *pe
		unplanned.CostSource = ""
		if c, err := eng.ComputeAt(&unplanned, at); err == nil {
			return c
		}
	}
	return cost
}
