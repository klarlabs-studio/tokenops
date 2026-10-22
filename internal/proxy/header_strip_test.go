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

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/internal/proxy/attributionbridge"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// headerRecorder is an upstream that keeps every header it receives.
type headerRecorder struct {
	mu   sync.Mutex
	last http.Header
}

func startHeaderRecorder(t *testing.T) (*httptest.Server, *headerRecorder) {
	t.Helper()
	rec := &headerRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		rec.mu.Lock()
		rec.last = r.Header.Clone()
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","content":[{"type":"text","text":"hi"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func (h *headerRecorder) headers() http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

func startStripProxy(t *testing.T, upstream string, observer bool) *Server {
	t.Helper()
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	anthropic, _ := providers.Lookup(eventschema.ProviderAnthropic)
	opts := []Option{
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{{Provider: anthropic, Upstream: u}}),
	}
	if observer {
		opts = append(opts, WithEventBus(&captureBus{}), WithTokenizer(tokenizer.NewRegistry()))
	}
	srv := New("127.0.0.1:0", opts...)
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())
	return srv
}

func assertNoTokenopsHeaders(t *testing.T, got http.Header) {
	t.Helper()
	if got == nil {
		t.Fatal("upstream not called")
	}
	for name := range got {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "X-Tokenops-") {
			t.Errorf("TokenOps header %q leaked upstream", name)
		}
	}
	if got.Get("x-api-key") != "test-key" {
		t.Errorf("provider credential not forwarded: x-api-key = %q", got.Get("x-api-key"))
	}
}

func TestProxyStripsTokenopsHeadersBeforeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name     string
		observer bool
	}{
		{name: "observer on", observer: true},
		{name: "observer off", observer: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, rec := startHeaderRecorder(t)
			srv := startStripProxy(t, upstream.URL, tc.observer)
			req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/anthropic/v1/messages",
				strings.NewReader(`{"model":"claude-fable-5","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-api-key", "test-key")
			req.Header.Set(headerWorkflowID, "wf:1")
			req.Header.Set(headerAgentID, "agent:1")
			req.Header.Set(headerSessionID, "session:1")
			req.Header.Set(headerUserID, "someone@example.test")
			req.Header.Set(headerExecutionID, "exec:1")
			req.Header.Set(headerCacheControl, "bypass")
			req.Header.Set("x-tokenops-future-header", "anything")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			assertNoTokenopsHeaders(t, rec.headers())
		})
	}
}

func TestAttributionBridgeHeadersStopAtTheProxy(t *testing.T) {
	upstream, rec := startHeaderRecorder(t)
	srv := startStripProxy(t, upstream.URL, true)
	handler, err := attributionbridge.NewHandler("http://"+srv.Addr(), "exec:bridge", "wf:bridge")
	if err != nil {
		t.Fatal(err)
	}
	bridge := httptest.NewServer(handler)
	defer bridge.Close()
	req, _ := http.NewRequest(http.MethodPost, bridge.URL+"/anthropic/v1/messages",
		strings.NewReader(`{"model":"claude-fable-5","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	assertNoTokenopsHeaders(t, rec.headers())
}
