package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sdk-shim-{openai,anthropic,gemini}: integration tests that send requests
// to the proxy using paths the official SDKs actually emit when their
// base-URL knob is pointed at TokenOps. They assert that:
//
//   1. The proxy forwards the request to the upstream with the correct
//      path (auth header + body intact).
//   2. Streaming responses pass through chunk-by-chunk.
//
// The mocked upstream stands in for api.openai.com / api.anthropic.com /
// generativelanguage.googleapis.com.

type recordingUpstream struct {
	t          *testing.T
	srv        *httptest.Server
	lastPath   atomic.Pointer[string]
	lastHdr    atomic.Pointer[http.Header]
	lastBody   atomic.Pointer[string]
	streamMode atomic.Bool
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	t.Helper()
	u := &recordingUpstream{t: t}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		path := r.URL.Path
		hdr := r.Header.Clone()
		bodyStr := string(body)
		u.lastPath.Store(&path)
		u.lastHdr.Store(&hdr)
		u.lastBody.Store(&bodyStr)

		if u.streamMode.Load() {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, _ := w.(http.Flusher)
			for i := 0; i < 3; i++ {
				_, _ = fmt.Fprintf(w, "event: message_delta\ndata: {\"i\":%d}\n\n", i)
				if flusher != nil {
					flusher.Flush()
				}
				time.Sleep(2 * time.Millisecond)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func startSDKProxy(t *testing.T, providerID, upstream string) string {
	t.Helper()
	routes, err := BuildProviderRoutes(map[string]string{providerID: upstream})
	if err != nil {
		t.Fatalf("BuildProviderRoutes: %v", err)
	}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes(routes),
	)
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
	return srv.Addr()
}

// TestSDKShimOpenAI mirrors what the OpenAI Python/Node SDKs send when
// OPENAI_BASE_URL is set to "<proxy>/openai/v1". The SDK appends
// "/chat/completions" so the wire path is "/openai/v1/chat/completions";
// the proxy must rewrite that to the upstream's "/v1/chat/completions"
// while preserving the bearer token.
func TestSDKShimOpenAI(t *testing.T) {
	upstream := newRecordingUpstream(t)
	addr := startSDKProxy(t, "openai", upstream.srv.URL)
	base := "http://" + addr + "/openai/v1"

	req, _ := http.NewRequest(http.MethodPost,
		base+"/chat/completions",
		strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-shim-test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tokenops-Workflow-Id", "wf-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	gotPath := *upstream.lastPath.Load()
	if gotPath != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
	gotHdr := *upstream.lastHdr.Load()
	if gotHdr.Get("Authorization") != "Bearer sk-shim-test" {
		t.Errorf("Authorization passthrough failed: %q", gotHdr.Get("Authorization"))
	}
	if gotHdr.Get("X-Tokenops-Workflow-Id") != "wf-1" {
		t.Errorf("attribution header lost: %q", gotHdr.Get("X-Tokenops-Workflow-Id"))
	}
}

// TestSDKShimAnthropic mirrors ANTHROPIC_BASE_URL="<proxy>/anthropic"
// with the SDK appending "/v1/messages". Validates auth + streaming.
func TestSDKShimAnthropic(t *testing.T) {
	upstream := newRecordingUpstream(t)
	upstream.streamMode.Store(true)
	addr := startSDKProxy(t, "anthropic", upstream.srv.URL)
	base := "http://" + addr + "/anthropic"

	req, _ := http.NewRequest(http.MethodPost,
		base+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-6","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "ant-shim-test")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected SSE content-type, got %q", ct)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("expected X-Accel-Buffering=no for SSE, got %q", got)
	}

	// Drain at least one SSE event before EOF; bufio guarantees we read
	// past the first flush.
	scanner := bufio.NewScanner(resp.Body)
	gotEvent := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			gotEvent = true
			break
		}
	}
	if !gotEvent {
		t.Errorf("no SSE event received from streaming proxy")
	}

	gotPath := *upstream.lastPath.Load()
	if gotPath != "/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/messages", gotPath)
	}
	gotHdr := *upstream.lastHdr.Load()
	if gotHdr.Get("x-api-key") != "ant-shim-test" {
		t.Errorf("x-api-key passthrough failed: %q", gotHdr.Get("x-api-key"))
	}
	if gotHdr.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("anthropic-version passthrough failed: %q", gotHdr.Get("anthropic-version"))
	}
}

// TestSDKShimGemini mirrors httpOptions.baseUrl = "<proxy>/gemini" with
// the SDK appending "/v1beta/models/<model>:generateContent".
func TestSDKShimGemini(t *testing.T) {
	upstream := newRecordingUpstream(t)
	addr := startSDKProxy(t, "gemini", upstream.srv.URL)
	base := "http://" + addr + "/gemini"

	req, _ := http.NewRequest(http.MethodPost,
		base+"/v1beta/models/gemini-1.5-pro:generateContent",
		strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	req.Header.Set("x-goog-api-key", "gem-shim-test")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	gotPath := *upstream.lastPath.Load()
	if gotPath != "/v1beta/models/gemini-1.5-pro:generateContent" {
		t.Errorf("upstream path = %q", gotPath)
	}
	gotHdr := *upstream.lastHdr.Load()
	if gotHdr.Get("x-goog-api-key") != "gem-shim-test" {
		t.Errorf("x-goog-api-key passthrough failed: %q", gotHdr.Get("x-goog-api-key"))
	}
}
