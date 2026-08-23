package daemon

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type stubReader []*eventschema.Envelope

func (s stubReader) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return s, nil
}

func codexQuotaEvent(usedPct string, at time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{
		Type:      eventschema.EventTypePrompt,
		Timestamp: at,
		Source:    "codex-jsonl",
		Attributes: map[string]string{
			"granularity":       "quota_snapshot",
			"primary_used_pct":  usedPct,
			"primary_resets_at": "0",
		},
		Payload: &eventschema.PromptEvent{
			Provider:   eventschema.ProviderOpenAI,
			CostSource: eventschema.CostSourcePlanIncluded,
		},
	}
}

// Codex reports its own 5-hour usage percentage, which is ground truth.
// Counting messages and dividing is a heuristic built for clients that
// publish nothing — using it where the vendor already answers would throw
// away the better signal.
func TestProbePrefersVendorReading(t *testing.T) {
	plan, ok := plans.Lookup("codex-plus")
	if !ok {
		t.Skip("codex-plus not in catalog")
	}
	got, ok := windowPctFor(context.Background(),
		stubReader{codexQuotaEvent("73.5", time.Now().Add(-time.Minute))},
		eventschema.ProviderOpenAI, plan, time.Now())
	if !ok {
		t.Fatal("a vendor reading must be usable")
	}
	if got != 73.5 {
		t.Errorf("pct = %v, want the vendor's 73.5", got)
	}
}

// Without a vendor reading it falls back to counting plan-included
// messages — which is all a client like Claude Code offers.
func TestProbeFallsBackToMessageCount(t *testing.T) {
	plan, ok := plans.Lookup("claude-max-20x")
	if !ok {
		t.Skip("claude-max-20x not in catalog")
	}
	now := time.Now()
	msgs := make(stubReader, 0, 40)
	for range 40 {
		msgs = append(msgs, &eventschema.Envelope{
			Type:      eventschema.EventTypePrompt,
			Timestamp: now.Add(-time.Minute),
			Source:    "claude-code-jsonl",
			Attributes: map[string]string{
				"granularity":         "assistant_turn",
				"starts_user_message": "true",
			},
			Payload: &eventschema.PromptEvent{
				Provider:   eventschema.ProviderAnthropic,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		})
	}
	got, ok := windowPctFor(context.Background(), msgs, eventschema.ProviderAnthropic, plan, now)
	if !ok {
		t.Fatal("the message-count fallback must still work")
	}
	if got != 20 {
		t.Errorf("pct = %v, want 20 (40 of 200)", got)
	}
}

// A plan with no window has nothing to report.
func TestProbeNoWindowPlan(t *testing.T) {
	if _, ok := windowPctFor(context.Background(), stubReader{},
		eventschema.ProviderAnthropic, plans.Plan{}, time.Now()); ok {
		t.Error("a plan without a rate-limit window must report nothing")
	}
}
