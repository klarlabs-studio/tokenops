package scorecard

import (
	"context"
	"sort"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// EventReader is the read-side port the scorecard depends on. Concrete
// sqlite-backed implementations satisfy it via the storeAdapter in
// service.go, but tests and future infrastructure adapters (ClickHouse,
// gRPC stream) can substitute their own without dragging the storage
// package into the domain.
type EventReader interface {
	// ReadEvents returns envelopes of the requested type whose timestamp
	// falls on or after since. The implementation is responsible for any
	// query limits (the scorecard does not paginate).
	ReadEvents(ctx context.Context, eventType eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error)
}

// LiveKPIs is the live-data variant of the wedge scorecard inputs. Each
// field carries both a value and a Computed flag — callers that lack the
// data needed to derive a KPI can fall through to operator-provided
// defaults rather than reporting a misleading zero.
type LiveKPIs struct {
	FVTSeconds       float64
	FVTComputed      bool
	TokenEfficiency  float64
	TEUComputed      bool
	SpendAttribution float64
	SACComputed      bool
}

// Compute walks the local event store over [since, now] and derives the
// three wedge KPIs:
//
//	FVT (First-Value Time): median wall-clock latency of the first
//	PromptEvent per session_id over the window. Sessions with only one
//	event still contribute their latency. Reported in seconds.
//
//	TEU (Token Efficiency Uplift): the percentage of optimizer-estimated
//	savings tokens relative to the sum of input tokens across the same
//	window. Computed as:
//	    sum(OptimizationEvent.EstimatedSavingsTokens) /
//	    sum(PromptEvent.InputTokens) * 100
//
//	SAC (Spend Attribution Completeness): the percentage of PromptEvents
//	carrying any attribution signal (workflow_id, agent_id, or session_id).
//
// When the store carries no relevant rows for a KPI, the corresponding
// *Computed flag stays false so the caller can substitute a manual or
// historical value.
func Compute(ctx context.Context, reader EventReader, since time.Time) (*LiveKPIs, error) {
	out := &LiveKPIs{}
	if reader == nil {
		return out, nil
	}
	prompts, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil {
		return nil, err
	}
	opts, err := reader.ReadEvents(ctx, eventschema.EventTypeOptimization, since)
	if err != nil {
		return nil, err
	}
	computeFVT(out, prompts)
	computeTEU(out, prompts, opts)
	computeSAC(out, prompts)
	return out, nil
}

func computeFVT(out *LiveKPIs, prompts []*eventschema.Envelope) {
	// Group by session_id (fallback to workflow_id when session is empty).
	type firstEntry struct {
		latency time.Duration
		seen    bool
	}
	bySession := map[string]firstEntry{}
	for _, env := range prompts {
		pe, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok {
			continue
		}
		key := pe.SessionID
		if key == "" {
			key = pe.WorkflowID
		}
		if key == "" {
			continue
		}
		if cur, exists := bySession[key]; exists && cur.seen {
			continue
		}
		bySession[key] = firstEntry{latency: pe.Latency, seen: true}
	}
	if len(bySession) == 0 {
		return
	}
	values := make([]float64, 0, len(bySession))
	for _, v := range bySession {
		values = append(values, v.latency.Seconds())
	}
	sort.Float64s(values)
	median := values[len(values)/2]
	out.FVTSeconds = median
	out.FVTComputed = true
}

func computeTEU(out *LiveKPIs, prompts []*eventschema.Envelope, opts []*eventschema.Envelope) {
	var input, saved int64
	for _, env := range prompts {
		if pe, ok := env.Payload.(*eventschema.PromptEvent); ok {
			input += pe.InputTokens
		}
	}
	for _, env := range opts {
		if oe, ok := env.Payload.(*eventschema.OptimizationEvent); ok {
			saved += oe.EstimatedSavingsTokens
		}
	}
	if input == 0 {
		return
	}
	out.TokenEfficiency = float64(saved) / float64(input) * 100
	out.TEUComputed = true
}

func computeSAC(out *LiveKPIs, prompts []*eventschema.Envelope) {
	if len(prompts) == 0 {
		return
	}
	attributed := 0
	for _, env := range prompts {
		pe, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok {
			continue
		}
		if pe.WorkflowID != "" || pe.AgentID != "" || pe.SessionID != "" {
			attributed++
		}
	}
	out.SpendAttribution = float64(attributed) / float64(len(prompts)) * 100
	out.SACComputed = true
}
