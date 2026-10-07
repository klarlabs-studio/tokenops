package headroom_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// promptReader holds plan-included prompts and nothing else.
type promptReader struct{ envs []*eventschema.Envelope }

func (r promptReader) ReadEvents(_ context.Context, t eventschema.EventType, _ time.Time) ([]*eventschema.Envelope, error) {
	if t != eventschema.EventTypePrompt {
		return nil, nil
	}
	return r.envs, nil
}

func (r promptReader) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{"proxy": int64(len(r.envs))}, nil
}

func messages(n int, at time.Time) []*eventschema.Envelope {
	out := make([]*eventschema.Envelope, 0, n)
	for range n {
		out = append(out, &eventschema.Envelope{
			Type: eventschema.EventTypePrompt, Timestamp: at, Source: "proxy",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: eventschema.CostSourcePlanIncluded},
		})
	}
	return out
}

func TestWindowPressureFromMessagesInTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cfg := cfgWith(map[string]string{"anthropic": "claude-pro"})
	pct, ok := headroom.WindowPressure(context.Background(), cfg, promptReader{messages(9, now.Add(-time.Hour))}, eventschema.ProviderAnthropic, now)
	if !ok || pct != 20 {
		t.Errorf("pressure %.1f%% (known %v), want 9 of claude-pro's 45 = 20%%", pct, ok)
	}
}

// Unknown, never zero, whenever the window cannot be read: a router rule
// gated on pressure must not fire on a default.
func TestWindowPressureIsUnknownWithoutAReading(t *testing.T) {
	now := time.Now()
	cases := map[string]struct {
		cfg    map[string]string
		reader headroom.EventReader
	}{
		"no plan bound":        {cfg: map[string]string{}, reader: promptReader{}},
		"no rate-limit window": {cfg: map[string]string{"anthropic": "pay-as-you-go"}, reader: promptReader{}},
		"unknown plan":         {cfg: map[string]string{"anthropic": "claude-ultra"}, reader: promptReader{}},
		"no event store":       {cfg: map[string]string{"anthropic": "claude-pro"}},
	}
	for name, tc := range cases {
		if pct, ok := headroom.WindowPressure(context.Background(), cfgWith(tc.cfg), tc.reader, eventschema.ProviderAnthropic, now); ok {
			t.Errorf("%s: pressure %.1f%%, want unknown", name, pct)
		}
	}
	if _, ok := headroom.WindowPressure(context.Background(), nil, promptReader{}, eventschema.ProviderAnthropic, now); ok {
		t.Error("nil config: want unknown")
	}
}

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
func TestWindowPressurePrefersTheVendorsReading(t *testing.T) {
	if _, ok := plans.Lookup("gpt-plus"); !ok {
		t.Skip("gpt-plus not in catalog")
	}
	got, ok := headroom.WindowPressure(context.Background(), cfgWith(map[string]string{"openai": "gpt-plus"}),
		stubReader{codexQuotaEvent("73.5", time.Now().Add(-time.Minute))},
		eventschema.ProviderOpenAI, time.Now())
	if !ok {
		t.Fatal("a vendor reading must be usable")
	}
	if got != 73.5 {
		t.Errorf("pct = %v, want the vendor's 73.5", got)
	}
}

// Without a vendor reading it falls back to counting plan-included
// messages — which is all a client like Claude Code offers.
func TestWindowPressureFallsBackToTheMessageCount(t *testing.T) {
	plan, ok := plans.Lookup("claude-max-20x")
	if !ok {
		t.Skip("claude-max-20x not in catalog")
	}
	now := time.Now()
	// A fifth of the allowance, expressed against the catalog rather than a
	// hardcoded 200: the Anthropic tiers derive from Pro, so the absolute
	// moves when that baseline is corrected.
	sent := int(plan.MessagesPerWindow / 5)
	msgs := make(stubReader, 0, sent)
	for range sent {
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
	got, ok := headroom.WindowPressure(context.Background(), cfgWith(map[string]string{"anthropic": "claude-max-20x"}),
		msgs, eventschema.ProviderAnthropic, now)
	if !ok {
		t.Fatal("the message-count fallback must still work")
	}
	if got != 20 {
		t.Errorf("pct = %v, want 20 (%d of %d)", got, sent, plan.MessagesPerWindow)
	}
}
