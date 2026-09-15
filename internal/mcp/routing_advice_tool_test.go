package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func adviceServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterRoutingAdviceTools(srv, RoutingAdviceDeps{Config: cfg}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return srv
}

func decodeAdvice(t *testing.T, out string) routingAdviceResult {
	t.Helper()
	var res routingAdviceResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode (%q): %v", out, err)
	}
	return res
}

// An abstention has to say why. "Stay" with no reason is
// indistinguishable from a broken optimizer, which is the failure this
// whole tool is meant to replace.
func TestRoutingAdviseExplainsEveryAbstention(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"no config at all", nil, "no configuration loaded"},
		{"routing not set up", &config.Config{}, "smart routing is off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := decodeAdvice(t, execTool(t, adviceServer(t, tc.cfg), "tokenops_routing_advise",
				map[string]any{"instruction": "fix it", "model": "gpt-4o", "provider": "openai"}))
			if res.Recommendation != "stay" {
				t.Fatalf("Recommendation = %q, want stay", res.Recommendation)
			}
			if res.Reason == "" {
				t.Fatal("abstained with no reason")
			}
			if !strings.Contains(res.Reason, tc.want) && !strings.Contains(res.Note, tc.want) {
				t.Errorf("reason %q / note %q does not mention %q", res.Reason, res.Note, tc.want)
			}
		})
	}
}

// The tool recommends; it never applies. On "stay" it still returns a
// model, so a caller can use one field either way — and that model is the
// one it was given.
func TestRoutingAdviseReturnsTheCallersModelOnStay(t *testing.T) {
	cfg := &config.Config{}
	cfg.Optimizer.SmartRouting.Enabled = true
	res := decodeAdvice(t, execTool(t, adviceServer(t, cfg), "tokenops_routing_advise",
		map[string]any{"instruction": "fix it", "model": "gpt-4o", "provider": "openai"}))
	if res.Model != "gpt-4o" {
		t.Errorf("Model = %q, want the model that was passed in", res.Model)
	}
	// No store is wired, so the window cannot be read — and an unmeasured
	// window is never treated as a full one.
	if res.Recommendation != "stay" {
		t.Errorf("Recommendation = %q, want stay with no window reading", res.Recommendation)
	}
	if res.WindowKnown {
		t.Error("WindowKnown = true with no store")
	}
	if res.Note == "" {
		t.Error("a weaker answer was reported as a considered one; the missing meter should be named")
	}
}

// The window reading is per provider. Answering from the wrong one is
// worse than asking, so several plans and no named provider is a refusal.
func TestRoutingAdviseRefusesToGuessTheProvider(t *testing.T) {
	cfg := &config.Config{Plans: map[string]string{"openai": "a", "anthropic": "b"}}
	cfg.Optimizer.SmartRouting.Enabled = true
	res := decodeAdvice(t, execTool(t, adviceServer(t, cfg), "tokenops_routing_advise",
		map[string]any{"instruction": "fix it", "model": "gpt-4o"}))
	if res.Recommendation != "stay" {
		t.Fatalf("Recommendation = %q, want stay", res.Recommendation)
	}
	if !strings.Contains(res.Reason, "no provider named") {
		t.Errorf("reason = %q, want it to say the provider is ambiguous", res.Reason)
	}
}

// With exactly one plan there is nothing to guess.
func TestRoutingAdviseInfersASingleProvider(t *testing.T) {
	cfg := &config.Config{Plans: map[string]string{"openai": "a"}}
	cfg.Optimizer.SmartRouting.Enabled = true
	res := decodeAdvice(t, execTool(t, adviceServer(t, cfg), "tokenops_routing_advise",
		map[string]any{"instruction": "fix it", "model": "gpt-4o"}))
	if strings.Contains(res.Reason, "no provider named") {
		t.Errorf("refused to infer the only configured provider: %q", res.Reason)
	}
}
