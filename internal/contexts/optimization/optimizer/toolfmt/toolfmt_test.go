package toolfmt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func newOpt() *Optimizer {
	reg := formatter.DefaultRegistry(formatter.LossPolicy{Default: formatter.LossBalanced})
	return New(Config{Level: formatter.LossBalanced, MinSavingsTokens: 1}, reg, nil)
}

// verboseGoTest is a chunk of go test output an agent would paste into its
// next turn as a tool result.
const verboseGoTest = "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n--- PASS: TestB (0.00s)\n=== RUN   TestC\n--- FAIL: TestC (0.01s)\n    c_test.go:9: boom\nPASS\nFAIL\tgithub.com/x/y\t0.1s\nok  \tgithub.com/x/z\t0.2s\n"

func TestOptimizer_AnthropicToolResultString(t *testing.T) {
	body := map[string]any{
		"model": "claude-sonnet-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "run the tests"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "t1",
					"content":     verboseGoTest,
				},
			}},
		},
	}
	raw, _ := json.Marshal(body)
	recs, err := newOpt().Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic,
		Body:     raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 recommendation, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Kind != eventschema.OptimizationTypeCommandFmt {
		t.Errorf("kind = %s", rec.Kind)
	}
	if rec.EstimatedSavingsTokens <= 0 {
		t.Error("no savings estimated")
	}
	// The rewritten body must still carry the FAIL line (critical) but not
	// the passing scaffolding.
	out := string(rec.ApplyBody)
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "c_test.go:9") {
		t.Errorf("critical failure signal lost:\n%s", out)
	}
	if strings.Contains(out, "=== RUN") {
		t.Errorf("scaffolding survived compression:\n%s", out)
	}
	// And the body must remain valid JSON.
	var probe map[string]any
	if err := json.Unmarshal(rec.ApplyBody, &probe); err != nil {
		t.Errorf("rewritten body is not valid JSON: %v", err)
	}
}

func TestOptimizer_OpenAIToolMessage(t *testing.T) {
	body := map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{"role": "assistant", "content": "calling tool"},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": verboseGoTest},
		},
	}
	raw, _ := json.Marshal(body)
	recs, err := newOpt().Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 recommendation, got %d", len(recs))
	}
	if !strings.Contains(string(recs[0].ApplyBody), "FAIL") {
		t.Error("critical line lost in OpenAI tool message")
	}
}

func TestOptimizer_NoToolOutput_NoRecommendation(t *testing.T) {
	body := map[string]any{
		"model": "claude-sonnet-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "just a normal prompt with no tool output"},
		},
	}
	raw, _ := json.Marshal(body)
	recs, err := newOpt().Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic,
		Body:     raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Errorf("expected no recommendation for tool-free body, got %d", len(recs))
	}
}

func TestOptimizer_UnsupportedProvider(t *testing.T) {
	recs, _ := newOpt().Run(context.Background(), &optimizer.Request{
		Provider: eventschema.Provider("gemini"),
		Body:     []byte(`{"messages":[]}`),
	})
	if recs != nil {
		t.Error("non-openai/anthropic provider should yield no recommendation")
	}
}

func TestOptimizer_Idempotent(t *testing.T) {
	// Compressing already-compact output yields no further recommendation.
	compact := "FAIL\tgithub.com/x/y\t0.1s\n"
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "tool", "content": compact},
		},
	}
	raw, _ := json.Marshal(body)
	recs, _ := newOpt().Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     raw,
	})
	if len(recs) != 0 {
		t.Errorf("already-compact output should not be recompressed, got %d recs", len(recs))
	}
}
