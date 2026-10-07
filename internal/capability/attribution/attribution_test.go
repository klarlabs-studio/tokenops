package attribution

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Before TokenOps knew endpoints, every Claude Code turn was Anthropic's
// and covered by the Max plan. FireConnect pointed Claude Code at
// Fireworks on the 20th. At start: the Fireworks model moves to
// Fireworks; Claude turns after the 20th went through FireRouter on an API
// key and become billed; Claude turns before it stay covered.
func TestCorrectGatewayAttributionFollowsTheRouteHistory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	switched := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	routes, err := routehistory.OpenAt(filepath.Join(home, "route-history.jsonl"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Observe(routehistory.HarnessClaudeCode, "", switched.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Observe(routehistory.HarnessClaudeCode, "https://api.fireworks.ai/inference", switched); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	mk := func(id, model string, at time.Time) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: claudecodejsonl.SourceTag,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: model, CostSource: eventschema.CostSourcePlanIncluded},
		}
	}
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{
		mk("kimi", "kimi-k3", switched.Add(time.Hour)),
		mk("claude-before", "claude-sonnet-5", switched.Add(-time.Hour)),
		mk("claude-after", "claude-sonnet-5", switched.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	now := time.Now().UTC()
	if _, err := CorrectGateway(ctx, cfg, store, routes, now); err != nil {
		t.Fatal(err)
	}
	if again, err := CorrectGateway(ctx, cfg, store, routes, now); err != nil || len(again) != 0 {
		t.Errorf("second pass corrected %+v, err %v", again, err)
	}

	got, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		provider eventschema.Provider
		source   eventschema.CostSource
		endpoint string
	}{
		"kimi":          {eventschema.ProviderFireworks, eventschema.CostSourceMetered, "fireworks"},
		"claude-before": {eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded, ""},
		"claude-after":  {eventschema.ProviderAnthropic, eventschema.CostSourceMetered, "fireworks"},
	}
	for _, env := range got {
		p := env.Payload.(*eventschema.PromptEvent)
		w := want[env.ID]
		if p.Provider != w.provider || p.CostSource != w.source || env.Attributes["endpoint"] != w.endpoint {
			t.Errorf("%s: provider %q, cost source %q, endpoint %q; want %+v",
				env.ID, p.Provider, p.CostSource, env.Attributes["endpoint"], w)
		}
	}
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func turn(id, source, session, provider, model string) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Source: source,
		Payload: &eventschema.PromptEvent{Provider: eventschema.Provider(provider), RequestModel: model,
			SessionID: session, CostSource: eventschema.CostSourcePlanIncluded},
	}
}

// A Codex session on Fireworks: Fireworks bills its own models, and runs
// OpenAI's on the operator's own credential, so those stay OpenAI's,
// billed per token through Fireworks. The correction decided per session
// and moved them to Fireworks at every start; it now puts them back.
func TestCorrectCodexDecidesPerModel(t *testing.T) {
	ctx := context.Background()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"),
		[]byte("[model_providers.fireworks]\nbase_url = \"https://api.fireworks.ai/inference/v1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rollout-s1.jsonl"),
		[]byte(`{"type":"session_meta","payload":{"id":"s1","model_provider":"fireworks"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	kimi := "accounts/fireworks/models/kimi-k2"
	live := turn("gpt-live", codexjsonl.SourceTag, "s1", "openai", "gpt-5-mini")
	live.Payload.(*eventschema.PromptEvent).CostSource = eventschema.CostSourceMetered
	live.Attributes = map[string]string{"endpoint": "fireworks"}
	moved := turn("gpt-moved", codexjsonl.SourceTag, "s1", "fireworks", "gpt-5-codex")
	moved.Payload.(*eventschema.PromptEvent).CostSource = eventschema.CostSourceMetered
	moved.Attributes = map[string]string{"endpoint": "fireworks"}
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{
		turn("kimi", codexjsonl.SourceTag, "s1", "openai", kimi),
		turn("gpt-legacy", codexjsonl.SourceTag, "s1", "openai", "gpt-5"),
		live, moved,
		turn("other-session", codexjsonl.SourceTag, "s2", "openai", kimi),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Plans: map[string]string{"openai": "chatgpt-plus"}}
	cfg.VendorUsage.CodexJSONL.Enabled = true
	cfg.VendorUsage.CodexJSONL.Root = root

	got, err := CorrectCodex(ctx, cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]int64{}
	for _, c := range got {
		calls[c.From+">"+c.To] += c.Calls
	}
	if len(calls) != 3 || calls["openai>fireworks"] != 1 || calls["openai>openai"] != 1 || calls["fireworks>openai"] != 1 {
		t.Errorf("corrections %v", calls)
	}
	if again, _ := CorrectCodex(ctx, cfg, store); len(again) != 0 {
		t.Errorf("second pass corrected %+v", again)
	}
	assertTurns(t, store, map[string]struct {
		provider eventschema.Provider
		source   eventschema.CostSource
	}{
		"kimi":          {eventschema.ProviderFireworks, eventschema.CostSourceMetered},
		"gpt-legacy":    {eventschema.ProviderOpenAI, eventschema.CostSourceMetered},
		"gpt-live":      {eventschema.ProviderOpenAI, eventschema.CostSourceMetered},
		"gpt-moved":     {eventschema.ProviderOpenAI, eventschema.CostSourceMetered},
		"other-session": {eventschema.ProviderOpenAI, eventschema.CostSourcePlanIncluded},
	})
	all, _ := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypePrompt})
	for _, env := range all {
		if env.ID != "other-session" && env.Attributes["endpoint"] != "fireworks" {
			t.Errorf("%s: endpoint %q, want fireworks", env.ID, env.Attributes["endpoint"])
		}
	}
}

func TestCorrectOpencodeRenamesRawProviderIDs(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{
		turn("zai", opencode.SourceTag, "s", "zai-coding-plan", "glm-5"),
		turn("anthropic", opencode.SourceTag, "s", "anthropic", "claude-sonnet-5"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := CorrectOpencode(ctx, store)
	if err != nil || len(got) != 1 || got[0].From != "zai-coding-plan" || got[0].To != "zai" {
		t.Fatalf("corrections %+v, err %v", got, err)
	}
	if again, _ := CorrectOpencode(ctx, store); len(again) != 0 {
		t.Errorf("second pass corrected %+v", again)
	}
	assertTurns(t, store, map[string]struct {
		provider eventschema.Provider
		source   eventschema.CostSource
	}{
		"zai":       {"zai", eventschema.CostSourcePlanIncluded},
		"anthropic": {eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded},
	})
}

func assertTurns(t *testing.T, store *sqlite.Store, want map[string]struct {
	provider eventschema.Provider
	source   eventschema.CostSource
}) {
	t.Helper()
	got, err := store.Query(context.Background(), sqlite.Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range got {
		p := env.Payload.(*eventschema.PromptEvent)
		if w := want[env.ID]; p.Provider != w.provider || p.CostSource != w.source {
			t.Errorf("%s: provider %q cost source %q, want %+v", env.ID, p.Provider, p.CostSource, w)
		}
	}
}
