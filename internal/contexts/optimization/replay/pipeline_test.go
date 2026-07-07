package replay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// End-to-end proxy-plane validation: a realistic Anthropic request whose
// tool_result carries verbose go-test output must, when run through the
// DEFAULT pipeline (not a hand-built one), surface a command_fmt
// OptimizationEvent with real token savings — proving the toolfmt optimizer
// is wired into the pipeline every proxy/replay path uses.
func TestDefaultPipeline_CommandFmtCompressesToolOutput(t *testing.T) {
	toolOutput := "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n" +
		"--- PASS: TestB (0.00s)\n=== RUN   TestC\n--- FAIL: TestC (0.01s)\n" +
		"    c_test.go:9: boom\nPASS\nFAIL\tgithub.com/x/y\t0.1s\nok  \tgithub.com/x/z\t0.2s\n"
	body := map[string]any{
		"model": "claude-sonnet-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "run the tests"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": toolOutput},
			}},
		},
	}
	raw, _ := json.Marshal(body)

	out, err := DefaultPipeline(nil).Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic,
		Model:    "claude-sonnet-5",
		Body:     raw,
		Mode:     optimizer.ModePassive,
	}, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	var fmtEvent *eventschema.OptimizationEvent
	for _, ev := range out.Events {
		if ev.Kind == eventschema.OptimizationTypeCommandFmt {
			fmtEvent = ev
		}
	}
	if fmtEvent == nil {
		t.Fatalf("no command_fmt event in %d pipeline events", len(out.Events))
	}
	if fmtEvent.EstimatedSavingsTokens <= 0 {
		t.Errorf("command_fmt reported no savings: %+v", fmtEvent)
	}
	// The reason should reflect a real byte reduction on the tool output.
	if !strings.Contains(fmtEvent.Reason, "tool output") {
		t.Errorf("unexpected reason: %q", fmtEvent.Reason)
	}
}

func TestDefaultPipelineHasFiveOptimizers(t *testing.T) {
	// prompt_compress, command_fmt, semantic_dedupe, retrieval_prune, context_trim.
	p := DefaultPipeline(nil)
	if got := len(p.Optimizers()); got != 5 {
		t.Errorf("optimizers = %d, want 5", got)
	}
}

func TestBuildPipelineRespectsDisable(t *testing.T) {
	p := BuildPipeline(nil, PipelineConfig{Disable: []string{"semantic_dedupe", "context_trim"}})
	if got := len(p.Optimizers()); got != 3 {
		t.Errorf("optimizers = %d, want 3 after disabling 2 of 5", got)
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
	if got := len(p.Optimizers()); got != 6 {
		t.Errorf("optimizers = %d, want 6 with router appended", got)
	}
}

func TestBuildPipelineSkipsRouterWithoutRules(t *testing.T) {
	p := BuildPipeline(nil, PipelineConfig{Routing: &router.Config{}})
	if got := len(p.Optimizers()); got != 5 {
		t.Errorf("optimizers = %d, want 5 when routing has no rules", got)
	}
}

// End-to-end: a replayed prompt matching a routing rule must surface a
// model_router recommendation carrying the $ delta between the models.
func TestRouterRecommendationCarriesUSDSavings(t *testing.T) {
	eng := spend.NewEngine(spend.DefaultTable())
	p := BuildPipeline(nil, PipelineConfig{
		Routing: &router.Config{Rules: []router.Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8*",
			ToModel: "claude-sonnet-4-6", Quality: 0.9,
		}}},
		Spend: eng,
	})
	req := &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
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
	// opus-4-8: 1M×$15 + 100K×$75/M = 22.50; sonnet-4-6: 1M×$3 + 100K×$15/M = 4.50
	want := 18.00
	if routed.EstimatedSavingsUSD < want-0.01 || routed.EstimatedSavingsUSD > want+0.01 {
		t.Errorf("savings = %.4f; want ~%.2f", routed.EstimatedSavingsUSD, want)
	}
	if routed.Reason != "route claude-opus-4-8 -> claude-sonnet-4-6" {
		t.Errorf("reason = %q", routed.Reason)
	}
}
