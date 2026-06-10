package replay

import (
	"context"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer/router"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestDefaultPipelineHasFourOptimizers(t *testing.T) {
	p := DefaultPipeline(nil)
	if got := len(p.Optimizers()); got != 4 {
		t.Errorf("optimizers = %d, want 4", got)
	}
}

func TestBuildPipelineRespectsDisable(t *testing.T) {
	p := BuildPipeline(nil, PipelineConfig{Disable: []string{"semantic_dedupe", "context_trim"}})
	if got := len(p.Optimizers()); got != 2 {
		t.Errorf("optimizers = %d, want 2 after disabling 2", got)
	}
}

func TestBuildPipelineIncludesRouterWhenRulesConfigured(t *testing.T) {
	p := BuildPipeline(nil, PipelineConfig{
		Routing: &router.Config{Rules: []router.Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-fable-5*",
			ToModel: "claude-opus-4-8", Quality: 0.9,
		}}},
		Spend: spend.NewEngine(spend.DefaultTable()),
	})
	if got := len(p.Optimizers()); got != 5 {
		t.Errorf("optimizers = %d, want 5 with router appended", got)
	}
}

func TestBuildPipelineSkipsRouterWithoutRules(t *testing.T) {
	p := BuildPipeline(nil, PipelineConfig{Routing: &router.Config{}})
	if got := len(p.Optimizers()); got != 4 {
		t.Errorf("optimizers = %d, want 4 when routing has no rules", got)
	}
}

// End-to-end: a replayed prompt matching a routing rule must surface a
// model_router recommendation carrying the $ delta between the models.
func TestRouterRecommendationCarriesUSDSavings(t *testing.T) {
	eng := spend.NewEngine(spend.DefaultTable())
	p := BuildPipeline(nil, PipelineConfig{
		Routing: &router.Config{Rules: []router.Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-fable-5*",
			ToModel: "claude-opus-4-8", Quality: 0.9,
		}}},
		Spend: eng,
	})
	req := &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-fable-5",
		InputTokens: 1_000_000, OutputTokens: 100_000,
		Mode: optimizer.ModeReplay,
	}
	out, err := p.Run(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	var routed *eventschema.OptimizationEvent
	for _, ev := range out.Events {
		if ev.Kind == eventschema.OptimizationTypeRouter {
			routed = ev
		}
	}
	if routed == nil {
		t.Fatalf("no model_router event in %d events", len(out.Events))
	}
	// fable: 1M×$10 + 100K×$50/M = 15.00; opus-4-8: 1M×$5 + 100K×$25/M = 7.50
	want := 7.50
	if routed.EstimatedSavingsUSD < want-0.01 || routed.EstimatedSavingsUSD > want+0.01 {
		t.Errorf("savings = %.4f; want ~%.2f", routed.EstimatedSavingsUSD, want)
	}
	if routed.Reason != "route claude-fable-5 -> claude-opus-4-8" {
		t.Errorf("reason = %q", routed.Reason)
	}
}
