package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer/router"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/providers"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
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

// Active mode: a request matching a routing rule reaches the upstream
// with the rewritten model, the observation keeps the original model,
// and an applied OptimizationEvent lands on the bus.
func TestActiveRoutingRewritesModel(t *testing.T) {
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if upstreamModel != "claude-opus-4-8" {
		t.Errorf("upstream model = %q; want claude-opus-4-8", upstreamModel)
	}

	envs := waitForEvent(t, bus, 2)
	var optEv *eventschema.OptimizationEvent
	var promptEv *eventschema.PromptEvent
	for _, env := range envs {
		switch p := env.Payload.(type) {
		case *eventschema.OptimizationEvent:
			optEv = p
		case *eventschema.PromptEvent:
			promptEv = p
		}
	}
	if optEv == nil {
		t.Fatal("no OptimizationEvent published for applied route")
	}
	if optEv.Kind != eventschema.OptimizationTypeRouter ||
		optEv.Decision != eventschema.OptimizationDecisionApplied ||
		optEv.Mode != eventschema.OptimizationModeInteractive {
		t.Errorf("optimization event = %+v", optEv)
	}
	if optEv.Reason != "route claude-fable-5 -> claude-opus-4-8" {
		t.Errorf("reason = %q", optEv.Reason)
	}
	if promptEv == nil {
		t.Fatal("no PromptEvent published")
	}
	if promptEv.RequestModel != "claude-fable-5" {
		t.Errorf("observation RequestModel = %q; want original claude-fable-5", promptEv.RequestModel)
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
