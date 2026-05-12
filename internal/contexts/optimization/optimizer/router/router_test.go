package router

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func bodyWithModel(t *testing.T, model string, extra map[string]any) []byte {
	t.Helper()
	m := map[string]any{"model": model, "messages": []any{}}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestRouteFromExpensiveToCheapEmitsSavings(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, spend.NewEngine(spend.DefaultTable()))

	body := bodyWithModel(t, "gpt-4o", nil)
	recs, err := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o", Body: body,
		InputTokens: 1_000_000, OutputTokens: 500_000,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	rec := recs[0]
	if !strings.Contains(rec.Reason, "gpt-4o -> gpt-4o-mini") {
		t.Errorf("reason: %q", rec.Reason)
	}
	if rec.QualityScore != 0.9 {
		t.Errorf("quality lost: %f", rec.QualityScore)
	}
	if rec.EstimatedSavingsUSD <= 0 {
		t.Errorf("expected positive USD savings, got %f", rec.EstimatedSavingsUSD)
	}
	// Verify rewritten body switches model field.
	var got map[string]any
	if err := json.Unmarshal(rec.ApplyBody, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["model"] != "gpt-4o-mini" {
		t.Errorf("model not rewritten: %v", got["model"])
	}
}

func TestPrefixRuleMatches(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o*", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o-2026-01",
		Body: bodyWithModel(t, "gpt-4o-2026-01", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("prefix should match, got %d", len(recs))
	}
}

func TestNoMatchEmitsNothing(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-3.5-turbo",
		Body: bodyWithModel(t, "gpt-3.5-turbo", nil),
	})
	if len(recs) != 0 {
		t.Errorf("expected no rec, got %+v", recs)
	}
}

func TestQualityBelowMinSilenced(t *testing.T) {
	r := New(Config{
		MinQuality: 0.8,
		Rules: []Rule{
			{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.5},
		},
	}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Body: bodyWithModel(t, "gpt-4o", nil),
	})
	if len(recs) != 0 {
		t.Errorf("low-quality rule should be silenced, got %+v", recs)
	}
}

func TestFallbackChain(t *testing.T) {
	r := New(Config{
		IsAvailable: func(_ eventschema.Provider, model string) bool {
			return model == "gpt-3.5-turbo" // primary unavailable, fallback ok
		},
		Rules: []Rule{
			{
				Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o",
				ToModel: "gpt-4o-mini", Fallbacks: []string{"o1", "gpt-3.5-turbo"},
				Quality: 0.85,
			},
		},
	}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Body: bodyWithModel(t, "gpt-4o", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("expected fallback rec, got %d", len(recs))
	}
	if !strings.Contains(recs[0].Reason, "-> gpt-3.5-turbo") {
		t.Errorf("expected fallback to gpt-3.5-turbo, got reason %q", recs[0].Reason)
	}
}

func TestAllTargetsUnavailable(t *testing.T) {
	r := New(Config{
		IsAvailable: func(eventschema.Provider, string) bool { return false },
		Rules: []Rule{
			{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o",
				ToModel: "gpt-4o-mini", Quality: 0.9},
		},
	}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Body: bodyWithModel(t, "gpt-4o", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("expected misroute rec, got %d", len(recs))
	}
	if recs[0].ApplyBody != nil {
		t.Errorf("ApplyBody should be nil when no target available: %q", recs[0].ApplyBody)
	}
	if !strings.Contains(recs[0].Reason, "no available target") {
		t.Errorf("reason: %q", recs[0].Reason)
	}
}

func TestRouteToMoreExpensiveReportsZeroSavings(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o-mini", ToModel: "gpt-4o", Quality: 0.99},
	}}, spend.NewEngine(spend.DefaultTable()))
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o-mini",
		InputTokens: 1_000_000, OutputTokens: 100_000,
		Body: bodyWithModel(t, "gpt-4o-mini", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("expected rec")
	}
	if recs[0].EstimatedSavingsUSD != 0 {
		t.Errorf("upgrade should not report savings, got %f", recs[0].EstimatedSavingsUSD)
	}
}

func TestPreservesTopLevelFields(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, nil)
	body := bodyWithModel(t, "gpt-4o", map[string]any{
		"temperature": 0.7,
		"tool_choice": "auto",
	})
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o", Body: body,
	})
	if len(recs) != 1 {
		t.Fatalf("rec missing")
	}
	var got map[string]any
	_ = json.Unmarshal(recs[0].ApplyBody, &got)
	if got["temperature"] != 0.7 || got["tool_choice"] != "auto" {
		t.Errorf("fields lost: %v", got)
	}
}

func TestNilRequestNoOp(t *testing.T) {
	r := New(Config{}, nil)
	recs, err := r.Run(context.Background(), nil)
	if err != nil || len(recs) != 0 {
		t.Errorf("nil req: %v / %+v", err, recs)
	}
}

func TestKindIsRouter(t *testing.T) {
	r := New(Config{}, nil)
	if got := r.Kind(); got != eventschema.OptimizationTypeRouter {
		t.Errorf("kind = %s", got)
	}
}
