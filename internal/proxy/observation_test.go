package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/internal/providers"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// captureBus records every published envelope in-memory for assertions.
type captureBus struct {
	mu        sync.Mutex
	envelopes []*eventschema.Envelope
	published int64
}

func (c *captureBus) Publish(env *eventschema.Envelope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.envelopes = append(c.envelopes, env)
	c.published++
}
func (c *captureBus) DroppedCount() int64   { return 0 }
func (c *captureBus) PublishedCount() int64 { c.mu.Lock(); defer c.mu.Unlock(); return c.published }
func (c *captureBus) Close(time.Duration) error {
	return nil
}

func (c *captureBus) snapshot() []*eventschema.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*eventschema.Envelope, len(c.envelopes))
	copy(out, c.envelopes)
	return out
}

var _ events.Bus = (*captureBus)(nil)

func startProxyForObservation(t *testing.T, upstream *httptest.Server) (string, *captureBus) {
	t.Helper()
	u, _ := url.Parse(upstream.URL)
	openai, _ := providers.Lookup(eventschema.ProviderOpenAI)
	route := ProviderRoute{Provider: openai, Upstream: u}

	bus := &captureBus{}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{route}),
		WithEventBus(bus),
		WithTokenizer(tokenizer.NewRegistry()),
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

func waitForEvent(t *testing.T, bus *captureBus, n int) []*eventschema.Envelope {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := bus.snapshot()
		if len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("only %d events after wait, want %d", len(bus.snapshot()), n)
	return nil
}

func TestObserverEmitsPromptEvent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "hello world") {
			t.Errorf("upstream did not see body: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","model":"gpt-4o-2024-08-06","choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	}))
	defer upstream.Close()

	base, bus := startProxyForObservation(t, upstream)

	req, _ := http.NewRequest(http.MethodPost,
		base+"/openai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o-2024-08-06","messages":[{"role":"user","content":"hello world"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tokenops-Workflow-Id", "wf-1")
	req.Header.Set("X-Tokenops-Agent-Id", "agent-a")
	req.Header.Set("X-Tokenops-Session-Id", "sess-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	envs := waitForEvent(t, bus, 1)
	env := envs[0]
	if env.Type != eventschema.EventTypePrompt {
		t.Fatalf("type = %s", env.Type)
	}
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderOpenAI {
		t.Errorf("provider = %s", pe.Provider)
	}
	if pe.RequestModel != "gpt-4o-2024-08-06" {
		t.Errorf("model = %q", pe.RequestModel)
	}
	if !strings.HasPrefix(pe.PromptHash, "sha256:") {
		t.Errorf("prompt hash format: %q", pe.PromptHash)
	}
	if pe.InputTokens <= 0 {
		t.Errorf("input tokens not estimated: %d", pe.InputTokens)
	}
	if pe.OutputTokens <= 0 {
		t.Errorf("output tokens not estimated: %d", pe.OutputTokens)
	}
	if pe.Status != 200 {
		t.Errorf("status = %d", pe.Status)
	}
	if pe.Latency <= 0 {
		t.Errorf("latency = %s", pe.Latency)
	}
	if pe.WorkflowID != "wf-1" || pe.AgentID != "agent-a" || pe.SessionID != "sess-1" {
		t.Errorf("attribution lost: %+v", pe)
	}
	if pe.Streaming {
		t.Errorf("non-SSE response should not be flagged streaming")
	}
}

func TestObserverFlagsStreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"delta\":\"hi\"}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	base, bus := startProxyForObservation(t, upstream)

	req, _ := http.NewRequest(http.MethodPost,
		base+"/openai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	envs := waitForEvent(t, bus, 1)
	pe := envs[0].Payload.(*eventschema.PromptEvent)
	if !pe.Streaming {
		t.Errorf("Streaming should be true for SSE response")
	}
	if pe.TimeToFirstToken <= 0 {
		t.Errorf("TTFT should be positive for streamed responses, got %s", pe.TimeToFirstToken)
	}
}

func TestObserverHashIsStableForIdenticalBodies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	base, bus := startProxyForObservation(t, upstream)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"same"}]}`
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost,
			base+"/openai/v1/chat/completions",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	envs := waitForEvent(t, bus, 2)
	a := envs[0].Payload.(*eventschema.PromptEvent)
	b := envs[1].Payload.(*eventschema.PromptEvent)
	if a.PromptHash != b.PromptHash {
		t.Errorf("hash differs for identical bodies: %s vs %s", a.PromptHash, b.PromptHash)
	}
}

func TestObserverDoesNotEmitForControlEndpoints(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	base, bus := startProxyForObservation(t, upstream)

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	_ = resp.Body.Close()
	time.Sleep(50 * time.Millisecond)
	if got := bus.PublishedCount(); got != 0 {
		t.Errorf("control endpoint emitted %d events", got)
	}
}
