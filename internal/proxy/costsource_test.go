package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// startProxyForCostSource boots a proxy whose Anthropic route is declared
// plan-covered, so emitted PromptEvents should carry the subscription
// billing basis rather than defaulting to metered.
func startProxyForCostSource(t *testing.T, covered map[eventschema.Provider]bool) (string, *captureBus) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	t.Cleanup(upstream.Close)

	u, _ := url.Parse(upstream.URL)
	anthropic, _ := providers.Lookup(eventschema.ProviderAnthropic)
	bus := &captureBus{}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{{Provider: anthropic, Upstream: u}}),
		WithEventBus(bus),
		WithTokenizer(tokenizer.NewRegistry()),
		WithCostEngine(spend.NewEngine(spend.DefaultTable())),
		WithPlanCoverage(func(p eventschema.Provider) bool { return covered[p] }),
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

func postMessage(t *testing.T, base string) {
	t.Helper()
	postMessageModel(t, base, "claude-opus-4-8")
}

func postMessageModel(t *testing.T, base, model string) {
	t.Helper()
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(base+"/anthropic/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func TestUnknownModelDoesNotClaimMeasuredCost(t *testing.T) {
	base, bus := startProxyForCostSource(t, nil)
	postMessageModel(t, base, "not-in-rate-card")
	waitForEvent(t, bus, 1)
	prompt := firstPromptEvent(t, bus)
	if prompt.CostMeasured {
		t.Fatal("missing rate-card row must not claim measured cost")
	}
}

func firstPromptEvent(t *testing.T, bus *captureBus) *eventschema.PromptEvent {
	t.Helper()
	for _, env := range bus.snapshot() {
		if pe, ok := env.Payload.(*eventschema.PromptEvent); ok {
			return pe
		}
	}
	t.Fatalf("no PromptEvent published")
	return nil
}

// A provider bound to a flat-rate plan must emit plan_included, so the
// spend engine prices it at the $0.00 the operator is actually billed
// instead of a list-price fiction.
func TestPlanCoveredProviderEmitsPlanIncluded(t *testing.T) {
	base, bus := startProxyForCostSource(t, map[eventschema.Provider]bool{
		eventschema.ProviderAnthropic: true,
	})
	postMessage(t, base)
	waitForEvent(t, bus, 1)

	if got := firstPromptEvent(t, bus).CostSource; got != eventschema.CostSourcePlanIncluded {
		t.Errorf("CostSource = %q, want %q", got, eventschema.CostSourcePlanIncluded)
	}
	if firstPromptEvent(t, bus).CostMeasured {
		t.Error("plan quota event must not claim measured metered cost")
	}
}

// Without a plan binding the provider is metered — the historical default.
func TestUnboundProviderStaysMetered(t *testing.T) {
	base, bus := startProxyForCostSource(t, nil)
	postMessage(t, base)
	waitForEvent(t, bus, 1)

	prompt := firstPromptEvent(t, bus)
	if got := prompt.CostSource; got != eventschema.CostSourceMetered {
		t.Errorf("CostSource = %q, want %q", got, eventschema.CostSourceMetered)
	}
	if !prompt.CostMeasured || prompt.CostUSD <= 0 {
		t.Errorf("known metered model should carry measured cost, got measured=%v cost=%v", prompt.CostMeasured, prompt.CostUSD)
	}
}
