package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/decide"
	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func startProxyForRouting(t *testing.T, upstream *httptest.Server, cfg router.Config) (string, *captureBus) {
	t.Helper()
	u, _ := url.Parse(upstream.URL)
	anthropic, _ := providers.Lookup(eventschema.ProviderAnthropic)
	route := ProviderRoute{Provider: anthropic, Upstream: u}

	bus := &captureBus{}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{route}),
		WithEventBus(bus),
		WithTokenizer(tokenizer.NewRegistry()),
		WithActiveRouting(cfg, spend.NewEngine(spend.DefaultTable())),
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())
	return "http://" + srv.Addr(), bus
}

// Autonomous routing without an outcome ledger must fail closed and explain
// that the proposed route was held back.
func TestActiveRoutingWithoutOutcomeLedgerPreservesBaseline(t *testing.T) {
	var upstreamModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		upstreamModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","content":[{"type":"text","text":"hi"}]}`)
	}))
	defer upstream.Close()

	base, bus := startProxyForRouting(t, upstream, router.Config{
		Rules: []router.Rule{{
			Provider:  eventschema.ProviderAnthropic,
			FromModel: "claude-fable-5*",
			ToModel:   "claude-opus-4-8",
			Quality:   0.9,
		}},
	})

	req, _ := http.NewRequest(http.MethodPost,
		base+"/anthropic/v1/messages",
		strings.NewReader(`{"model":"claude-fable-5","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerSessionID, "route-session")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if upstreamModel != "claude-fable-5" {
		t.Errorf("upstream model = %q; want unchanged baseline claude-fable-5", upstreamModel)
	}

	envs := waitForEvent(t, bus, 2)
	var optEv *eventschema.OptimizationEvent
	var promptEv *eventschema.PromptEvent
	for _, env := range envs {
		switch p := env.Payload.(type) {
		case *eventschema.OptimizationEvent:
			optEv = p
			if env.Association.Actor != "session:route-session" {
				t.Errorf("optimization association = %+v; want session actor for verification join", env.Association)
			}
		case *eventschema.PromptEvent:
			promptEv = p
		}
	}
	if optEv == nil {
		t.Fatal("no OptimizationEvent published for held route")
	}
	if optEv.Kind != eventschema.OptimizationTypeRouter ||
		optEv.Decision != eventschema.OptimizationDecisionSkipped ||
		optEv.Mode != eventschema.OptimizationModeInteractive {
		t.Errorf("optimization event = %+v", optEv)
	}
	if !strings.Contains(optEv.Reason, "outcome history unavailable") {
		t.Errorf("reason = %q; want fail-closed explanation", optEv.Reason)
	}
	if promptEv == nil {
		t.Fatal("no PromptEvent published")
	}
	if promptEv.RequestModel != "claude-fable-5" {
		t.Errorf("observation RequestModel = %q; want original claude-fable-5", promptEv.RequestModel)
	}
}

func TestRoutingExperimentRunsOneBaselineAndOneVariant(t *testing.T) {
	var models []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		models = append(models, body.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","content":[{"type":"text","text":"hi"}]}`)
	}))
	defer upstream.Close()

	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := experiments.New(store)
	provider, baseline, variant := eventschema.ProviderAnthropic, "claude-fable-5", "claude-opus-4-8"
	if _, err := manager.Start(context.Background(), experiments.StartInput{
		Provider: string(provider), BaselineModel: baseline, VariantModel: variant, MaxPairs: 1,
		Fingerprint: decide.RouteFingerprint(provider, baseline, variant, "proxy"),
	}); err != nil {
		t.Fatal(err)
	}

	u, _ := url.Parse(upstream.URL)
	anthropic, _ := providers.Lookup(provider)
	bus := &captureBus{}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{{Provider: anthropic, Upstream: u}}),
		WithEventBus(bus), WithTokenizer(tokenizer.NewRegistry()), WithExperiments(manager),
		WithActiveRouting(router.Config{Rules: []router.Rule{{Provider: provider, FromModel: baseline, ToModel: variant, Quality: .9}}}, spend.NewEngine(spend.DefaultTable())),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = srv.Shutdown(shutdown)
	}()
	waitListening(t, srv.Addr())
	for range 3 {
		req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/anthropic/v1/messages", strings.NewReader(`{"model":"claude-fable-5","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if len(models) != 3 || models[0] == models[1] || models[2] != baseline {
		t.Fatalf("assignments = %v; want one paired baseline/variant then baseline while evidence is untrusted", models)
	}
}

// Requests that match no rule pass through byte-identical.
func TestActiveRoutingPassThroughOnNoMatch(t *testing.T) {
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		upstreamBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1"}`)
	}))
	defer upstream.Close()

	base, bus := startProxyForRouting(t, upstream, router.Config{
		Rules: []router.Rule{{
			Provider:  eventschema.ProviderAnthropic,
			FromModel: "claude-3-5-sonnet*",
			ToModel:   "claude-haiku-4-5",
			Quality:   0.9,
		}},
	})

	orig := `{"model":"claude-fable-5","max_tokens":10,"messages":[{"role":"user","content":"x"}]}`
	resp, err := http.Post(base+"/anthropic/v1/messages", "application/json", strings.NewReader(orig))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if upstreamBody != orig {
		t.Errorf("body altered without matching rule:\n got %s\nwant %s", upstreamBody, orig)
	}
	for _, env := range waitForEvent(t, bus, 1) {
		if env.Type == eventschema.EventTypeOptimization {
			t.Errorf("unexpected optimization event: %+v", env.Payload)
		}
	}
}
