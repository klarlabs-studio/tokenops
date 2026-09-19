package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// tightWindowDeps binds anthropic to a windowed plan and records a
// usage-meter reading at 95% of the five-hour window, so a mechanical turn
// gets as far as the pricing table.
func tightWindowDeps(t *testing.T) RoutingAdviceDeps {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "r.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reading := &eventschema.Envelope{
		ID: "meter", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: time.Now().UTC().Add(-time.Minute),
		Source:     "claude-usage-meter",
		Attributes: map[string]string{"five_hour_used_pct": "95"},
		Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, Status: 200},
	}
	if err := st.Append(context.Background(), reading); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	cfg.Optimizer.SmartRouting.Enabled = true
	return RoutingAdviceDeps{Config: cfg, Store: st}
}

func adviseOpus(t *testing.T, d RoutingAdviceDeps) routingAdviceResult {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterRoutingAdviceTools(srv, d); err != nil {
		t.Fatal(err)
	}
	return decodeAdvice(t, execTool(t, srv, "tokenops_routing_advise",
		map[string]any{"instruction": "fix it", "model": "claude-opus-5", "tool_density": 0.9}))
}

// The description promises "what the pricing table currently calls
// cheapest", but the tool built its own engine over the compiled-in table
// while the rest of serve priced with the daemon-refreshed card. A model
// the live card knows and the binary does not was invisible to the advice.
func TestRoutingAdvisePricesWithTheInjectedEngine(t *testing.T) {
	d := tightWindowDeps(t)
	d.Spend = spend.NewEngine(spend.Table{Rates: map[spend.Key]spend.Rate{
		{Provider: eventschema.ProviderAnthropic, Model: "claude-opus-5"}:    {InputPerMillion: 15, OutputPerMillion: 75},
		{Provider: eventschema.ProviderAnthropic, Model: "claude-live-card"}: {InputPerMillion: 0.01, OutputPerMillion: 0.02},
	}})
	res := adviseOpus(t, d)
	if res.Recommendation != "switch" || res.Model != "claude-live-card" {
		t.Fatalf("advice = %s %q (%s), want a switch to the live card's cheapest model",
			res.Recommendation, res.Model, res.Reason)
	}
}

// Zero-value deps stay valid: no engine falls back to the compiled-in
// table rather than to "no priced alternative".
func TestRoutingAdviseFallsBackToTheDefaultTable(t *testing.T) {
	res := adviseOpus(t, tightWindowDeps(t))
	if res.Recommendation != "switch" || res.Model == "" || res.Model == "claude-live-card" {
		t.Fatalf("advice = %s %q (%s), want a switch priced from the default table",
			res.Recommendation, res.Model, res.Reason)
	}
}

// Config hot-reloads now, so "reload your MCP server" sent the operator to
// do something that changes nothing.
func TestRoutingAdviseSetupNoteDoesNotAskForAReload(t *testing.T) {
	res := decodeAdvice(t, execTool(t, adviceServer(t, nil), "tokenops_routing_advise",
		map[string]any{"instruction": "fix it", "model": "gpt-4o", "provider": "openai"}))
	if strings.Contains(res.Note, "reload your MCP server") {
		t.Errorf("note still tells the operator to reload: %q", res.Note)
	}
}
