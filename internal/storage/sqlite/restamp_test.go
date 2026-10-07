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

func TestRestampMeteredIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{
		mustPromptEnvelope(t, "covered", t0, &eventschema.PromptEvent{Provider: "anthropic", CostSource: eventschema.CostSourcePlanIncluded}),
	}); err != nil {
		t.Fatal(err)
	}
	from, to := time.Unix(0, 0).UTC(), t0.Add(time.Hour)
	if n, _ := s.RestampMetered(ctx, "anthropic", from, to); n != 1 {
		t.Fatalf("first pass %d, want 1", n)
	}
	if n, _ := s.RestampMetered(ctx, "anthropic", from, to); n != 0 {
		t.Errorf("second pass changed %d; the daemon runs this at every start", n)
	}
}

func TestReattributeSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mk := func(id, session, model string) *eventschema.Envelope {
		e := mustPromptEnvelope(t, id, at, &eventschema.PromptEvent{Provider: "openai", RequestModel: model, SessionID: session, CostSource: eventschema.CostSourcePlanIncluded})
		e.Source = "codex-jsonl"
		return e
	}
	kimi := "accounts/fireworks/models/kimi-k2"
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{mk("a1", "a", kimi), mk("a2", "a", kimi), mk("a3", "a", "gpt-5"), mk("b1", "b", kimi)}); err != nil {
		t.Fatal(err)
	}
	models, err := s.SessionModels(ctx, "codex-jsonl", "openai", "a")
	if err != nil || len(models) != 2 || models[0] != kimi || models[1] != "gpt-5" {
		t.Fatalf("session models %v, err %v", models, err)
	}
	n, err := s.ReattributeSession(ctx, "codex-jsonl", "a", kimi, "openai", "fireworks", "fireworks", true)
	if err != nil || n != 2 {
		t.Fatalf("moved %d, err %v; want 2", n, err)
	}
	if n, _ := s.ReattributeSession(ctx, "codex-jsonl", "a", kimi, "openai", "fireworks", "fireworks", true); n != 0 {
		t.Errorf("second pass moved %d", n)
	}
	got, _ := s.Query(ctx, Filter{Type: eventschema.EventTypePrompt})
	for _, env := range got {
		p := env.Payload.(*eventschema.PromptEvent)
		moved := env.ID == "a1" || env.ID == "a2"
		if (p.Provider == "fireworks") != moved || (p.CostSource == eventschema.CostSourceMetered) != moved {
			t.Errorf("%s: provider %q cost source %q", env.ID, p.Provider, p.CostSource)
		}
		if moved && env.Attributes["endpoint"] != "fireworks" {
			t.Errorf("%s: endpoint %q", env.ID, env.Attributes["endpoint"])
		}
	}
}

// Moving a turn to the provider it already has records its endpoint and
// uncovers it, once: an OpenAI model run through a gateway stays OpenAI's
// but is billed per token. A second pass finds nothing left to change.
func TestReattributeSessionToTheSameProviderMarksTheEndpointOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	e := mustPromptEnvelope(t, "g", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		&eventschema.PromptEvent{Provider: "openai", RequestModel: "gpt-5", SessionID: "a", CostSource: eventschema.CostSourcePlanIncluded})
	e.Source = "codex-jsonl"
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{e}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReattributeSession(ctx, "codex-jsonl", "a", "gpt-5", "openai", "openai", "fireworks", true); err != nil || n != 1 {
		t.Fatalf("first pass %d, err %v; want 1", n, err)
	}
	if n, _ := s.ReattributeSession(ctx, "codex-jsonl", "a", "gpt-5", "openai", "openai", "fireworks", true); n != 0 {
		t.Errorf("second pass changed %d; the daemon runs this at every start", n)
	}
	got, _ := s.Query(ctx, Filter{Type: eventschema.EventTypePrompt})
	p := got[0].Payload.(*eventschema.PromptEvent)
	if p.Provider != "openai" || p.CostSource != eventschema.CostSourceMetered || got[0].Attributes["endpoint"] != "fireworks" {
		t.Errorf("provider %q cost source %q endpoint %q", p.Provider, p.CostSource, got[0].Attributes["endpoint"])
	}
}

func TestRenameProvider(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	e := mustPromptEnvelope(t, "o1", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), &eventschema.PromptEvent{Provider: "zai-coding-plan"})
	e.Source = "opencode"
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{e}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ProvidersFor(ctx, "opencode"); len(got) != 1 || got[0] != "zai-coding-plan" {
		t.Fatalf("ProvidersFor = %v", got)
	}
	if n, err := s.RenameProvider(ctx, "opencode", "zai-coding-plan", "zai", "zai"); err != nil || n != 1 {
		t.Fatalf("renamed %d, err %v", n, err)
	}
	got, _ := s.Query(ctx, Filter{Type: eventschema.EventTypePrompt})
	if p := got[0].Payload.(*eventschema.PromptEvent); p.Provider != "zai" || got[0].Attributes["endpoint"] != "zai" {
		t.Errorf("after rename: provider %q endpoint %q", p.Provider, got[0].Attributes["endpoint"])
	}
}
