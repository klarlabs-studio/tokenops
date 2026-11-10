package daemon

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	anthropicusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type captureSink struct{ envs []*eventschema.Envelope }

func (c *captureSink) AppendBatch(_ context.Context, envs []*eventschema.Envelope) error {
	c.envs = append(c.envs, envs...)
	return nil
}

func TestPlanStampSink(t *testing.T) {
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-ultra"}}
	next := &captureSink{}
	sink := newPlanStampSink(next, cfg)

	mk := func(provider eventschema.Provider, cs eventschema.CostSource) *eventschema.Envelope {
		return &eventschema.Envelope{
			Type:    eventschema.EventTypePrompt,
			Payload: &eventschema.PromptEvent{Provider: provider, CostSource: cs},
		}
	}
	envs := []*eventschema.Envelope{
		mk(eventschema.ProviderAnthropic, ""),                          // → stamped
		mk(eventschema.ProviderAnthropic, eventschema.CostSourceTrial), // explicit → untouched
		mk(eventschema.ProviderOpenAI, ""),                             // no plan → untouched
		{Type: eventschema.EventTypeOptimization},                      // non-prompt → ignored
	}
	if err := sink.AppendBatch(context.Background(), envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	get := func(i int) eventschema.CostSource {
		return next.envs[i].Payload.(*eventschema.PromptEvent).CostSource
	}
	if get(0) != eventschema.CostSourcePlanIncluded {
		t.Errorf("anthropic empty → %q; want plan_included", get(0))
	}
	if get(1) != eventschema.CostSourceTrial {
		t.Errorf("explicit trial overwritten: %q", get(1))
	}
	if get(2) != "" {
		t.Errorf("unplanned provider stamped: %q", get(2))
	}
}

// The Anthropic Admin API usage report is what Anthropic billed the
// organisation's API keys. With a Claude subscription also bound, its
// buckets were stamped as covered: real spend left billed spend, and its
// tokens were added to the plan's use. The sink decides by the source, so
// a spend report that leaves its cost source open is not stamped either.
func TestPlanStampSinkLeavesASpendReportBilled(t *testing.T) {
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-ultra"}}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	bucket := func() *eventschema.Envelope {
		env, ok := anthropicusage.NewEnvelope(now.Add(-time.Hour), now, anthropicusage.UsageResult{
			Model: "claude-opus-5-5", UncachedInputTokens: 1000, OutputTokens: 100,
		})
		if !ok {
			t.Fatal("no envelope for a bucket with usage")
		}
		return env
	}
	asRecorded, open := bucket(), bucket()
	open.Payload.(*eventschema.PromptEvent).CostSource = ""
	planTurn := &eventschema.Envelope{
		Type: eventschema.EventTypePrompt, Timestamp: now.Add(-time.Minute), Source: "claude-code-jsonl",
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, TotalTokens: 50},
	}
	next := &captureSink{}
	if err := newPlanStampSink(next, cfg).AppendBatch(context.Background(), []*eventschema.Envelope{asRecorded, open, planTurn}); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]*eventschema.Envelope{"as recorded": asRecorded, "cost source left open": open} {
		if got := env.Payload.(*eventschema.PromptEvent).CostSource; got == eventschema.CostSourcePlanIncluded {
			t.Errorf("usage report bucket (%s) stamped as covered by the plan", name)
		}
	}
	if got := planTurn.Payload.(*eventschema.PromptEvent).CostSource; got != eventschema.CostSourcePlanIncluded {
		t.Fatalf("the plan's own turn stamped %q, want plan_included", got)
	}
	used, err := plans.ConsumptionFor(context.Background(), stored(next.envs), "anthropic", now)
	if err != nil {
		t.Fatal(err)
	}
	if used.ConsumedTokens != 50 {
		t.Errorf("plan use %d tokens, want the plan turn's 50: the report's tokens are an API key's", used.ConsumedTokens)
	}
}

// stored reads back what a sink was given.
type stored []*eventschema.Envelope

func (s stored) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for _, env := range s {
		if !env.Timestamp.Before(since) {
			out = append(out, env)
		}
	}
	return out, nil
}

func TestPlanStampSinkPassthroughWithoutPlans(t *testing.T) {
	next := &captureSink{}
	s := newPlanStampSink(next, config.Config{})
	if s != events.Sink(next) {
		t.Error("no plans configured should return next unchanged")
	}
}

// Usage-based Enterprise is billed at API rates from the first token.
// Stamping its usage as covered reported real spend as $0 and its spend
// limit as 0% used.
func TestSpendPlanUsageIsNotStampedCovered(t *testing.T) {
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-enterprise", "openai": "gpt-pro-5x"}}
	if got := bootstrap.PlanCostSource(cfg, eventschema.ProviderAnthropic); got != "" {
		t.Errorf("enterprise poller cost source %q, want billed", got)
	}
	next := &captureSink{}
	sink := newPlanStampSink(next, cfg)
	envs := []*eventschema.Envelope{
		{Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}},
		{Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI}},
	}
	if err := sink.AppendBatch(context.Background(), envs); err != nil {
		t.Fatal(err)
	}
	if got := envs[0].Payload.(*eventschema.PromptEvent).CostSource; got != "" {
		t.Errorf("enterprise usage stamped %q", got)
	}
	if got := envs[1].Payload.(*eventschema.PromptEvent).CostSource; got != eventschema.CostSourcePlanIncluded {
		t.Errorf("subscription usage stamped %q, want plan_included", got)
	}
}
