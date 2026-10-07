package daemon

import (
	"context"
	"testing"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type captureSink struct{ envs []*eventschema.Envelope }

func (c *captureSink) AppendBatch(_ context.Context, envs []*eventschema.Envelope) error {
	c.envs = append(c.envs, envs...)
	return nil
}

func TestPlanStampSink(t *testing.T) {
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
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
