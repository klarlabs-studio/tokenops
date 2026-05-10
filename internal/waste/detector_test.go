package waste

import (
	"fmt"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/workflow"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func mkStep(idx int, agent, hash string, inputTokens int64, ctxDelta int64) workflow.Step {
	return workflow.Step{
		Index: idx,
		Envelope: &eventschema.Envelope{
			ID: "e", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: time.Now().UTC(),
			Payload: &eventschema.PromptEvent{
				PromptHash: hash, Provider: eventschema.ProviderOpenAI,
				RequestModel: "gpt-4o", InputTokens: inputTokens,
				AgentID: agent, WorkflowID: "wf-A",
			},
		},
		Prompt: &eventschema.PromptEvent{
			PromptHash: hash, Provider: eventschema.ProviderOpenAI,
			RequestModel: "gpt-4o", InputTokens: inputTokens,
			AgentID: agent, WorkflowID: "wf-A",
		},
		ContextDelta: ctxDelta,
	}
}

func mkTrace(steps []workflow.Step) *workflow.Trace {
	t := &workflow.Trace{WorkflowID: "wf-A", Steps: steps, StepCount: len(steps)}
	for _, s := range steps {
		if s.Prompt.InputTokens > t.MaxContextSize {
			t.MaxContextSize = s.Prompt.InputTokens
		}
		if s.ContextDelta > 0 {
			t.ContextGrowthTotal += s.ContextDelta
		}
	}
	return t
}

func TestDetectsOversizedContext(t *testing.T) {
	d := New(Config{MaxContextTokens: 1000})
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-1", "h1", 500, 500),
		mkStep(1, "agent-1", "h2", 1500, 1000),
	})
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Kind == eventschema.CoachingKindTrimContext && ev.Summary == "Oversized context window" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected oversized context coaching, got %+v", events)
	}
}

func TestDetectsRunawayGrowth(t *testing.T) {
	d := New(Config{ContextGrowthLimitTokens: 100, MaxContextTokens: 1_000_000})
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-1", "h1", 50, 50),
		mkStep(1, "agent-1", "h2", 150, 100),
		mkStep(2, "agent-1", "h3", 350, 200),
	})
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Summary == "Runaway context growth" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected runaway growth coaching, got %+v", events)
	}
}

func TestDetectsAgentLoop(t *testing.T) {
	d := New(Config{MaxConsecutiveAgentLoops: 3, MaxContextTokens: 1_000_000, ContextGrowthLimitTokens: 1_000_000})
	steps := []workflow.Step{}
	for i := 0; i < 5; i++ {
		steps = append(steps, mkStep(i, "agent-A", fmt.Sprintf("h-%d", i), 10, 0))
	}
	trace := mkTrace(steps)
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Kind == eventschema.CoachingKindBreakRecursion {
			found = true
			if ev.AgentID != "agent-A" {
				t.Errorf("agent attribution wrong: %s", ev.AgentID)
			}
		}
	}
	if !found {
		t.Errorf("expected agent loop coaching, got %+v", events)
	}
}

func TestDetectsRecursion(t *testing.T) {
	d := New(Config{MaxContextTokens: 1_000_000, ContextGrowthLimitTokens: 1_000_000, MaxConsecutiveAgentLoops: 100})
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-A", "h-1", 10, 0),
		mkStep(1, "agent-A", "h-1", 10, 0), // duplicate hash
	})
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Kind == eventschema.CoachingKindReuseCache {
			found = true
			if ev.ReplayMetadata["prompt_hash"] != "h-1" {
				t.Errorf("prompt_hash metadata: %s", ev.ReplayMetadata["prompt_hash"])
			}
		}
	}
	if !found {
		t.Errorf("expected recursion coaching, got %+v", events)
	}
}

func TestNilOrEmptyTraceNoOp(t *testing.T) {
	d := New(Config{})
	if events := d.Detect(nil); events != nil {
		t.Errorf("nil trace: %+v", events)
	}
	if events := d.Detect(&workflow.Trace{}); events != nil {
		t.Errorf("empty trace: %+v", events)
	}
}

func TestHealthyTraceProducesNoFindings(t *testing.T) {
	d := New(Config{}) // defaults — high thresholds
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-A", "h-1", 100, 0),
		mkStep(1, "agent-B", "h-2", 150, 50),
		mkStep(2, "agent-A", "h-3", 200, 50),
	})
	if events := d.Detect(trace); len(events) != 0 {
		t.Errorf("healthy trace produced findings: %+v", events)
	}
}

func TestDefaultsApplied(t *testing.T) {
	d := New(Config{})
	if d.cfg.MaxContextTokens != 32_768 {
		t.Errorf("default max context = %d", d.cfg.MaxContextTokens)
	}
	if d.cfg.ContextGrowthLimitTokens != 16_384 {
		t.Errorf("default growth = %d", d.cfg.ContextGrowthLimitTokens)
	}
	if d.cfg.MaxConsecutiveAgentLoops != 4 {
		t.Errorf("default loops = %d", d.cfg.MaxConsecutiveAgentLoops)
	}
}
