package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/proxy/cache"
)

// counterUpstream returns an httptest server that increments a shared
// counter on every call so tests can assert how many times the upstream
// was actually contacted.
func counterUpstream(t *testing.T, body, contentType string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		calls.Add(1)
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func startCacheProxy(t *testing.T, upstream string, c *cache.Cache) string {
	t.Helper()
	routes, err := BuildProviderRoutes(map[string]string{"openai": upstream})
	if err != nil {
		t.Fatalf("BuildProviderRoutes: %v", err)
	}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes(routes),
		WithCache(c),
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

func TestProxyCacheServesHitWithoutUpstream(t *testing.T) {
	upstream, calls := counterUpstream(t, `{"ok":true}`, "application/json")
	c := cache.New(cache.Options{})
	addr := startCacheProxy(t, upstream.URL, c)

	url := "http://" + addr + "/openai/v1/chat/completions"
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`

	// First call populates cache.
	resp1 := mustPost(t, url, body, nil)
	if resp1.StatusCode != 200 {
		t.Fatalf("first status = %d", resp1.StatusCode)
	}
	if got := resp1.Header.Get("X-Tokenops-Cache-Status"); got == "hit" {
		t.Errorf("first response cache-status should not be hit: %q", got)
	}
	resp1.Body.Close()
	if calls.Load() != 1 {
		t.Fatalf("first call should reach upstream, got %d", calls.Load())
	}

	// Second identical call should be served from cache.
	resp2 := mustPost(t, url, body, nil)
	if resp2.StatusCode != 200 {
		t.Fatalf("second status = %d", resp2.StatusCode)
	}
	if got := resp2.Header.Get("X-Tokenops-Cache-Status"); got != "hit" {
		t.Errorf("expected cache-status=hit, got %q", got)
	}
	resp2.Body.Close()
	if calls.Load() != 1 {
		t.Errorf("upstream should not be called again, got %d", calls.Load())
	}
	if c.Metrics().Hits == 0 {
		t.Errorf("cache hit counter not incremented: %+v", c.Metrics())
	}
}

func TestProxyCacheBypassHeaderSkipsLookupAndStore(t *testing.T) {
	upstream, calls := counterUpstream(t, `{"ok":true}`, "application/json")
	c := cache.New(cache.Options{})
	addr := startCacheProxy(t, upstream.URL, c)

	url := "http://" + addr + "/openai/v1/chat/completions"
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`

	headers := map[string]string{"X-Tokenops-Cache": "bypass"}
	resp1 := mustPost(t, url, body, headers)
	resp1.Body.Close()
	resp2 := mustPost(t, url, body, headers)
	if got := resp2.Header.Get("X-Tokenops-Cache-Status"); got != "bypass" {
		t.Errorf("cache-status = %q, want bypass", got)
	}
	resp2.Body.Close()
	if calls.Load() != 2 {
		t.Errorf("bypass should hit upstream twice, got %d", calls.Load())
	}
	if c.Metrics().Bypasses == 0 {
		t.Errorf("bypass counter not incremented: %+v", c.Metrics())
	}
}

func TestProxyCacheRefreshHeaderForcesUpstream(t *testing.T) {
	upstream, calls := counterUpstream(t, `{"ok":true}`, "application/json")
	c := cache.New(cache.Options{})
	addr := startCacheProxy(t, upstream.URL, c)

	url := "http://" + addr + "/openai/v1/chat/completions"
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`

	resp1 := mustPost(t, url, body, nil)
	resp1.Body.Close()

	// refresh: skip lookup but populate cache.
	resp2 := mustPost(t, url, body, map[string]string{"X-Tokenops-Cache": "refresh"})
	if got := resp2.Header.Get("X-Tokenops-Cache-Status"); got != "refresh" && got != "store" {
		t.Errorf("cache-status = %q, want refresh|store", got)
	}
	resp2.Body.Close()

	if calls.Load() != 2 {
		t.Errorf("refresh should hit upstream, got %d", calls.Load())
	}

	// Subsequent identical request should be served from refreshed entry.
	resp3 := mustPost(t, url, body, nil)
	if got := resp3.Header.Get("X-Tokenops-Cache-Status"); got != "hit" {
		t.Errorf("post-refresh cache-status = %q, want hit", got)
	}
	resp3.Body.Close()
	if calls.Load() != 2 {
		t.Errorf("third call should be cached, got %d", calls.Load())
	}
}

func TestProxyCacheSkipsStreamingResponses(t *testing.T) {
	upstream, calls := counterUpstream(t, "data: {\"x\":1}\n\n", "text/event-stream")
	c := cache.New(cache.Options{})
	addr := startCacheProxy(t, upstream.URL, c)

	url := "http://" + addr + "/openai/v1/chat/completions"
	body := `{"stream":true,"messages":[]}`

	for i := 0; i < 2; i++ {
		resp := mustPost(t, url, body, nil)
		resp.Body.Close()
	}
	if calls.Load() != 2 {
		t.Errorf("streaming should never be cached, got %d upstream calls", calls.Load())
	}
	if m := c.Metrics(); m.Stores != 0 {
		t.Errorf("streaming response stored: %+v", m)
	}
}

func TestProxyCacheDoesNotCacheNon2xx(t *testing.T) {
	c := cache.New(cache.Options{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"err":"boom"}`)
	}))
	t.Cleanup(srv.Close)
	addr := startCacheProxy(t, srv.URL, c)

	url := "http://" + addr + "/openai/v1/chat/completions"
	body := `{"model":"x"}`
	resp := mustPost(t, url, body, nil)
	resp.Body.Close()
	if c.Metrics().Stores != 0 {
		t.Errorf("5xx response should not be cached: %+v", c.Metrics())
	}
}

// mustPost is a tiny helper used by the cache tests so they read clean.
func mustPost(t *testing.T, url, body string, hdrs map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}
