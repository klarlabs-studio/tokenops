package plans

import (
	"context"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// fakeReader feeds canned envelopes to ConsumptionFor so the math is
// exercised without sqlite.
type fakeReader struct{ envs []*eventschema.Envelope }

func (f fakeReader) ReadEvents(_ context.Context, _ eventschema.EventType, _ time.Time) ([]*eventschema.Envelope, error) {
	return f.envs, nil
}

func envAt(ts time.Time, provider string, tokens int64, source eventschema.CostSource) *eventschema.Envelope {
	return &eventschema.Envelope{
		Type:      eventschema.EventTypePrompt,
		Timestamp: ts,
		Payload: &eventschema.PromptEvent{
			Provider:     eventschema.Provider(provider),
			InputTokens:  tokens / 2,
			OutputTokens: tokens - (tokens / 2),
			TotalTokens:  tokens,
			CostSource:   source,
		},
	}
}

func TestConsumptionFilterByCostSourceAndProvider(t *testing.T) {
	now := time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
	monthStart := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)

	r := fakeReader{envs: []*eventschema.Envelope{
		envAt(monthStart.Add(48*time.Hour), "anthropic", 1000, eventschema.CostSourcePlanIncluded), // counted
		envAt(monthStart.Add(96*time.Hour), "anthropic", 500, eventschema.CostSourceMetered),       // skipped (metered)
		envAt(monthStart.Add(120*time.Hour), "openai", 700, eventschema.CostSourcePlanIncluded),    // skipped (wrong provider)
		envAt(now.Add(-2*time.Hour), "anthropic", 300, eventschema.CostSourcePlanIncluded),         // counted, also in week window
	}}

	got, err := ConsumptionFor(context.Background(), r, "anthropic", now)
	if err != nil {
		t.Fatalf("ConsumptionFor: %v", err)
	}
	if got.ConsumedTokens != 1300 {
		t.Errorf("ConsumedTokens=%d want 1300", got.ConsumedTokens)
	}
	// The 1000-token event is on May 3 — outside the week window
	// ending May 15. Only the 300-token event counts toward 7-day.
	if got.Last7DayTokens != 300 {
		t.Errorf("Last7DayTokens=%d want 300", got.Last7DayTokens)
	}
}

func TestConsumptionEmptyOnNoMatches(t *testing.T) {
	now := time.Date(2026, time.May, 15, 0, 0, 0, 0, time.UTC)
	r := fakeReader{envs: nil}
	got, err := ConsumptionFor(context.Background(), r, "anthropic", now)
	if err != nil {
		t.Fatalf("ConsumptionFor: %v", err)
	}
	if got.ConsumedTokens != 0 || got.Last7DayTokens != 0 {
		t.Errorf("expected zero consumption, got %+v", got)
	}
}
