package coaching

import (
	"context"
	"errors"
	"testing"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// fakeBackend implements llm.Backend without touching the network.
type fakeBackend struct {
	out string
	err error
}

func (f fakeBackend) Generate(_ context.Context, _, _ string) (string, error) {
	return f.out, f.err
}
func (f fakeBackend) Name() string { return "fake" }

func TestEnricherPromotesLLMOutputToSummary(t *testing.T) {
	enr := NewLLMEnricher(fakeBackend{out: "Trim the system prompt by 800 tokens to cut latency."})
	ev := &eventschema.CoachingEvent{
		Kind:    eventschema.CoachingKindReducePromptSize,
		Summary: "system prompt is 6500 tokens (threshold 4000)",
	}
	if err := enr.Enrich(context.Background(), ev); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if ev.Summary != "Trim the system prompt by 800 tokens to cut latency." {
		t.Errorf("Summary not promoted: %q", ev.Summary)
	}
	if ev.Details != "system prompt is 6500 tokens (threshold 4000)" {
		t.Errorf("heuristic summary lost — should move to Details, got %q", ev.Details)
	}
}

func TestEnricherKeepsHeuristicWhenBackendErrors(t *testing.T) {
	enr := NewLLMEnricher(fakeBackend{err: errors.New("rate limit")})
	ev := &eventschema.CoachingEvent{
		Kind:    eventschema.CoachingKindTrimContext,
		Summary: "context grew 1200 tokens between steps 4 and 7",
	}
	if err := enr.Enrich(context.Background(), ev); err == nil {
		t.Error("expected error to bubble so Pipeline can log it")
	}
	if ev.Summary == "" {
		t.Error("heuristic summary erased on backend error")
	}
}

func TestEnricherEmptyOutputLeavesEventUnchanged(t *testing.T) {
	enr := NewLLMEnricher(fakeBackend{out: "   "})
	ev := &eventschema.CoachingEvent{
		Kind:    eventschema.CoachingKindReuseCache,
		Summary: "repeated identical prompt hash 4x in 60s",
	}
	if err := enr.Enrich(context.Background(), ev); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if ev.Summary != "repeated identical prompt hash 4x in 60s" {
		t.Errorf("Summary altered on empty backend output: %q", ev.Summary)
	}
}

func TestNewLLMEnricherNilBackendReturnsNil(t *testing.T) {
	if got := NewLLMEnricher(nil); got != nil {
		t.Errorf("NewLLMEnricher(nil) = %v, want nil", got)
	}
}
