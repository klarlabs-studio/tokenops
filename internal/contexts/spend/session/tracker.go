// Package session records evidence of operator activity inside an MCP
// session. Every tracked MCP tool invocation lands as a synthetic
// PromptEvent with CostSource=plan_included so the rest of the
// analytics pipeline (ConsumptionFor, ConsumptionInWindow) treats it
// uniformly alongside real proxy traffic.
//
// The activity signal is intentionally a heuristic. It captures the
// fact that the operator is actively working with TokenOps inside an
// AI session; it does NOT measure the host LLM's own usage. A better
// signal — vendor /usage endpoint ingestion — is queued as a follow-up
// task. Until then, this signal is good enough to validate the wedge
// with real users.
package session

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Tracker records MCP tool invocations as plan_included events on the
// shared events bus. Safe for concurrent use; the bus owns the
// downstream fan-out (sqlite, OTLP, etc.).
type Tracker struct {
	bus     events.Bus
	mu      sync.Mutex
	counter map[string]int64
}

// Options configure the Tracker at construction time. Provider is the
// default eventschema.Provider value stamped on synthetic events;
// callers usually derive it from the single Config.Plans binding when
// only one plan is configured. SourceLabel populates Envelope.Source
// so dashboards can filter out (or in) MCP-session events.
type Options struct {
	Provider    eventschema.Provider
	SourceLabel string
}

// New returns a Tracker that publishes to bus. Pass a nil bus during
// tests; Record degrades to in-memory counting only.
func New(bus events.Bus, _ Options) *Tracker {
	return &Tracker{
		bus:     bus,
		counter: map[string]int64{},
	}
}

// Record stamps a single MCP tool invocation as a plan_included
// PromptEvent and increments the in-memory counter for diagnostics.
// Token counts are placeholder estimates (input 500, output 200) so
// downstream cost math never produces zero-token rows that confuse the
// burn-rate aggregator.
func (t *Tracker) Record(_ context.Context, opts Options, toolName string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.counter[toolName]++
	t.mu.Unlock()

	if t.bus == nil {
		return
	}
	now := time.Now().UTC()
	env := &eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     now,
		Source:        sourceLabel(opts.SourceLabel),
		Attributes: map[string]string{
			"mcp.tool": toolName,
		},
		Payload: &eventschema.PromptEvent{
			Provider:     opts.Provider,
			RequestModel: "mcp-session",
			InputTokens:  500,
			OutputTokens: 200,
			TotalTokens:  700,
			Status:       200,
			CostSource:   eventschema.CostSourcePlanIncluded,
		},
	}
	t.bus.Publish(env)
}

// Counts returns a snapshot of per-tool invocation counts. Used by
// tests and the future tokenops_session_budget tool for a sanity
// check on activity level.
func (t *Tracker) Counts() map[string]int64 {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]int64, len(t.counter))
	for k, v := range t.counter {
		out[k] = v
	}
	return out
}

func sourceLabel(s string) string {
	if s == "" {
		return "mcp-session"
	}
	return s
}
