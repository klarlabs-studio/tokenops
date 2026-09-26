package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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
	return startProxyForProviderObservation(t, upstream, eventschema.ProviderOpenAI)
}

func startProxyForProviderObservation(t *testing.T, upstream *httptest.Server, providerID eventschema.Provider) (string, *captureBus) {
	t.Helper()
	u, _ := url.Parse(upstream.URL)
	provider, _ := providers.Lookup(providerID)
	route := ProviderRoute{Provider: provider, Upstream: u}

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

func TestObserverClassifiesAnthropicErrorsWithoutRetainingContent(t *testing.T) {
	secret := "operator-private-prompt"
	tests := []struct {
		name       string
		status     int
		body       string
		want       string
		compressed bool
	}{
		{
			name:   "sampling parameter incompatibility",
			status: http.StatusBadRequest,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"temperature is not supported for this model: ` + secret + `"}}`,
			want:   "anthropic.invalid_request.unsupported_sampling_parameter",
		},
		{
			name:   "manual thinking incompatibility",
			status: http.StatusBadRequest,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"thinking with budget_tokens is not supported: ` + secret + `"}}`,
			want:   "anthropic.invalid_request.incompatible_thinking",
		},
		{
			name:       "compressed SSE sampling incompatibility",
			status:     http.StatusBadRequest,
			body:       "event: error\n" + `data: {"type":"error","error":{"type":"invalid_request_error","message":"top_p is not accepted for this model: ` + secret + `"}}` + "\n\n",
			want:       "anthropic.invalid_request.unsupported_sampling_parameter",
			compressed: true,
		},
		{
			name:   "unknown provider error type",
			status: http.StatusBadRequest,
			body:   `{"type":"error","error":{"type":"` + secret + `","message":"` + secret + `"}}`,
			want:   "anthropic.http_400",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.compressed {
					w.Header().Set("Content-Encoding", "gzip")
				}
				w.WriteHeader(tc.status)
				if tc.compressed {
					zw := gzip.NewWriter(w)
					_, _ = io.WriteString(zw, tc.body)
					_ = zw.Close()
				} else {
					_, _ = io.WriteString(w, tc.body)
				}
			}))
			defer upstream.Close()

			base, bus := startProxyForProviderObservation(t, upstream, eventschema.ProviderAnthropic)
			resp, err := http.Post(base+"/anthropic/v1/messages", "application/json",
				strings.NewReader(`{"model":"claude-sonnet-5","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			pe := waitForEvent(t, bus, 1)[0].Payload.(*eventschema.PromptEvent)
			if pe.Status != tc.status || pe.ErrorCode != tc.want {
				t.Fatalf("status/error = %d/%q, want %d/%q", pe.Status, pe.ErrorCode, tc.status, tc.want)
			}
			encoded, err := json.Marshal(pe)
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			if bytes.Contains(encoded, []byte(secret)) {
				t.Fatalf("event retained provider message content: %s", encoded)
			}
		})
	}
}

func TestClassifyOpaqueAnthropic400FromRequestShape(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{`{"thinking":{"type":"enabled","budget_tokens":1024}}`, "anthropic.http_400.with_manual_thinking"},
		{`{"temperature":0.2}`, "anthropic.http_400.with_sampling_parameter"},
		{`{"messages":[]}`, "anthropic.http_400.after_model_route"},
	}
	for _, tc := range tests {
		manual, sampling := anthropicCompatibilityTraits([]byte(tc.body))
		got := classifyUpstreamError(eventschema.ProviderAnthropic, 400, []byte("opaque"), "claude-sonnet-5", manual, sampling)
		if got != tc.want {
			t.Errorf("classification = %q, want %q", got, tc.want)
		}
	}
}

func TestObserverParsesCompressedStreamingUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = io.WriteString(zw, "event: message_start\n")
		_, _ = io.WriteString(zw, `data: {"type":"message_start","message":{"model":"claude-sonnet-5","usage":{"input_tokens":120,"cache_read_input_tokens":80}}}`+"\n\n")
		_, _ = io.WriteString(zw, "event: content_block_start\n")
		_, _ = io.WriteString(zw, `data: {"type":"content_block_start","content_block":{"type":"tool_use"}}`+"\n\n")
		_, _ = io.WriteString(zw, "event: message_delta\n")
		_, _ = io.WriteString(zw, `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":30}}`+"\n\n")
		_ = zw.Close()
	}))
	defer upstream.Close()

	base, bus := startProxyForProviderObservation(t, upstream, eventschema.ProviderAnthropic)
	resp, err := http.Post(base+"/anthropic/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-opus-5-5","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	pe := waitForEvent(t, bus, 1)[0].Payload.(*eventschema.PromptEvent)
	if pe.ResponseModel != "claude-sonnet-5" || pe.InputTokens != 120 || pe.CachedInputTokens != 80 || pe.OutputTokens != 30 {
		t.Fatalf("compressed usage/model not recovered: %+v", pe)
	}
	if pe.ToolCallCount != 1 || pe.TokenProvenance() != eventschema.TokenSourceVendorReported {
		t.Fatalf("compressed tool/provenance = %d/%q", pe.ToolCallCount, pe.TokenProvenance())
	}
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
	// Windows' default monotonic clock has ~15ms resolution; an observed
	// localhost round-trip can land in the same tick and report 0. Only
	// fail on an impossible negative duration.
	if pe.Latency < 0 {
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
		// Windows time.Now() resolution is ~15ms; without this delay
		// the proxy observer captures the request start and first
		// chunk inside the same tick and TTFT reads 0. The sleep
		// guarantees the clock advances so the assertion below means
		// something on every platform.
		time.Sleep(25 * time.Millisecond)
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

func TestObserverRecordsAuthoritativeStreamingUsageAndToolCalls(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "event: response.output_item.done\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","item":{"type":"function_call","name":"shell"}}`+"\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "event: response.completed\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"model":"gpt-6-sol-2026-09-01","status":"completed","usage":{"input_tokens":120,"output_tokens":30,"input_tokens_details":{"cached_tokens":80}},"output":[{"type":"function_call"},{"type":"message"}]}}`+"\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	base, bus := startProxyForObservation(t, upstream)
	req, _ := http.NewRequest(http.MethodPost,
		base+"/openai/v1/responses",
		strings.NewReader(`{"model":"gpt-6-sol","stream":true,"input":"inspect the repository","tools":[{"type":"function","name":"shell"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	pe := waitForEvent(t, bus, 1)[0].Payload.(*eventschema.PromptEvent)
	if !pe.Streaming || pe.ResponseModel != "gpt-6-sol-2026-09-01" {
		t.Errorf("stream/model = %v/%q", pe.Streaming, pe.ResponseModel)
	}
	if pe.InputTokens != 120 || pe.OutputTokens != 30 || pe.CachedInputTokens != 80 {
		t.Errorf("usage = input %d output %d cached %d", pe.InputTokens, pe.OutputTokens, pe.CachedInputTokens)
	}
	if pe.TokenProvenance() != eventschema.TokenSourceVendorReported {
		t.Errorf("token source = %q", pe.TokenProvenance())
	}
	if pe.ToolCallCount != 1 || pe.FinishReason != "completed" {
		t.Errorf("tool calls/finish = %d/%q", pe.ToolCallCount, pe.FinishReason)
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

func (c *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	c.Publish(env)
	return nil
}
