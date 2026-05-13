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

func TestConsumptionInWindowCountsRecentMessagesOnly(t *testing.T) {
	now := time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
	r := fakeReader{envs: []*eventschema.Envelope{
		// 6h ago — outside 5h window
		envAt(now.Add(-6*time.Hour), "anthropic", 1000, eventschema.CostSourcePlanIncluded),
		// 2h ago, in window
		envAt(now.Add(-2*time.Hour), "anthropic", 500, eventschema.CostSourcePlanIncluded),
		// 1h ago, metered — excluded
		envAt(now.Add(-time.Hour), "anthropic", 700, eventschema.CostSourceMetered),
		// 30m ago, wrong provider — excluded
		envAt(now.Add(-30*time.Minute), "openai", 300, eventschema.CostSourcePlanIncluded),
		// 10m ago, in window
		envAt(now.Add(-10*time.Minute), "anthropic", 200, eventschema.CostSourcePlanIncluded),
	}}
	got, err := ConsumptionInWindow(context.Background(), r, "anthropic", now, 5*time.Hour)
	if err != nil {
		t.Fatalf("ConsumptionInWindow: %v", err)
	}
	if got.MessagesInWindow != 2 {
		t.Errorf("MessagesInWindow=%d want 2", got.MessagesInWindow)
	}
	if got.TokensInWindow != 700 {
		t.Errorf("TokensInWindow=%d want 700", got.TokensInWindow)
	}
}

func TestConsumptionDropsDemoSource(t *testing.T) {
	now := time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
	// Two plan-included events for the same provider; one is tagged
	// Source="demo" (from `tokenops demo`) and must NOT count.
	demo := envAt(now.Add(-1*time.Hour), "anthropic", 1000, eventschema.CostSourcePlanIncluded)
	demo.Source = "demo"
	real := envAt(now.Add(-1*time.Hour), "anthropic", 500, eventschema.CostSourcePlanIncluded)
	real.Source = "mcp-session"
	r := fakeReader{envs: []*eventschema.Envelope{demo, real}}

	got, err := ConsumptionFor(context.Background(), r, "anthropic", now)
	if err != nil {
		t.Fatalf("ConsumptionFor: %v", err)
	}
	if got.ConsumedTokens != 500 {
		t.Errorf("ConsumedTokens=%d want 500 (demo dropped)", got.ConsumedTokens)
	}

	win, err := ConsumptionInWindow(context.Background(), r, "anthropic", now, 5*time.Hour)
	if err != nil {
		t.Fatalf("ConsumptionInWindow: %v", err)
	}
	if win.MessagesInWindow != 1 {
		t.Errorf("MessagesInWindow=%d want 1 (demo dropped)", win.MessagesInWindow)
	}
}

func TestConsumptionInWindowZeroDurationReturnsEmpty(t *testing.T) {
	now := time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
	r := fakeReader{envs: []*eventschema.Envelope{
		envAt(now.Add(-time.Hour), "anthropic", 100, eventschema.CostSourcePlanIncluded),
	}}
	got, err := ConsumptionInWindow(context.Background(), r, "anthropic", now, 0)
	if err != nil {
		t.Fatalf("ConsumptionInWindow: %v", err)
	}
	if got.MessagesInWindow != 0 || got.TokensInWindow != 0 {
		t.Errorf("expected zero (no window), got %+v", got)
	}
}
