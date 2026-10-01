package sqlite

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestRestampPlanIncluded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mustPromptEnvelope(t, "metered-in", t0, &eventschema.PromptEvent{Provider: "openai", RequestModel: "gpt-5.6-sol", CostUSD: 0.18, CostMeasured: true}),
		mustPromptEnvelope(t, "explicit-metered", t0.Add(time.Minute), &eventschema.PromptEvent{Provider: "openai", CostSource: eventschema.CostSourceMetered}),
		mustPromptEnvelope(t, "trial", t0, &eventschema.PromptEvent{Provider: "openai", CostSource: eventschema.CostSourceTrial}),
		mustPromptEnvelope(t, "other-provider", t0, &eventschema.PromptEvent{Provider: "anthropic"}),
		mustPromptEnvelope(t, "outside", t0.Add(48*time.Hour), &eventschema.PromptEvent{Provider: "openai"}),
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	n, err := s.RestampPlanIncluded(ctx, "openai", t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("restamped %d, err %v; want 2", n, err)
	}
	got, err := s.Query(ctx, Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range got {
		p := env.Payload.(*eventschema.PromptEvent)
		wantPlan := env.ID == "metered-in" || env.ID == "explicit-metered"
		if (p.CostSource == eventschema.CostSourcePlanIncluded) != wantPlan {
			t.Errorf("%s: cost source %q", env.ID, p.CostSource)
		}
		if wantPlan && (p.CostUSD != 0 || p.CostMeasured) {
			t.Errorf("%s kept its metered cost: %+v", env.ID, p)
		}
	}
	// Idempotent.
	if n, _ := s.RestampPlanIncluded(ctx, "openai", t0.Add(-time.Hour), t0.Add(time.Hour)); n != 0 {
		t.Errorf("second pass changed %d", n)
	}
}

func TestRestampMetered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mustPromptEnvelope(t, "covered", t0, &eventschema.PromptEvent{Provider: "anthropic", CostSource: eventschema.CostSourcePlanIncluded}),
		mustPromptEnvelope(t, "billed", t0, &eventschema.PromptEvent{Provider: "anthropic", CostUSD: 0.4, CostMeasured: true}),
		mustPromptEnvelope(t, "trial", t0, &eventschema.PromptEvent{Provider: "anthropic", CostSource: eventschema.CostSourceTrial}),
		mustPromptEnvelope(t, "other-provider", t0, &eventschema.PromptEvent{Provider: "openai", CostSource: eventschema.CostSourcePlanIncluded}),
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	n, err := s.RestampMetered(ctx, "anthropic", t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("restamped %d, err %v; want 1", n, err)
	}
	got, err := s.Query(ctx, Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range got {
		p := env.Payload.(*eventschema.PromptEvent)
		switch env.ID {
		case "covered":
			if p.CostSource != eventschema.CostSourceMetered {
				t.Errorf("covered: cost source %q, want metered", p.CostSource)
			}
		case "billed":
			if p.CostUSD != 0.4 || !p.CostMeasured {
				t.Errorf("billed lost its measured cost: %+v", p)
			}
		case "trial", "other-provider":
			if p.CostSource == eventschema.CostSourceMetered {
				t.Errorf("%s was re-marked", env.ID)
			}
		}
	}
}
