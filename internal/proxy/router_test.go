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
)

func TestBuildProviderRoutesDefault(t *testing.T) {
	routes, err := BuildProviderRoutes(nil)
	if err != nil {
		t.Fatalf("BuildProviderRoutes: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("want 3 routes, got %d", len(routes))
	}
}

func TestBuildProviderRoutesOverride(t *testing.T) {
	routes, err := BuildProviderRoutes(map[string]string{
		"openai": "https://example.test",
	})
	if err != nil {
		t.Fatalf("BuildProviderRoutes: %v", err)
	}
	for _, r := range routes {
		if r.Provider.ID == "openai" && r.Upstream.Host != "example.test" {
			t.Errorf("override not applied: %v", r.Upstream)
		}
	}
}

func TestBuildProviderRoutesUnknownOverride(t *testing.T) {
	if _, err := BuildProviderRoutes(map[string]string{"nope": "https://x.test"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestBuildProviderRoutesInvalidURL(t *testing.T) {
	if _, err := BuildProviderRoutes(map[string]string{"openai": "::not-a-url"}); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

type captured struct {
	method string
	path   string
	host   string
	auth   string
	apiKey string
	gKey   string
	body   string
}

func startUpstream(t *testing.T, respBody string) (*httptest.Server, *atomic.Pointer[captured]) {
	t.Helper()
	last := &atomic.Pointer[captured]{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		c := &captured{
			method: r.Method,
			path:   r.URL.Path,
			host:   r.Host,
			auth:   r.Header.Get("Authorization"),
			apiKey: r.Header.Get("x-api-key"),
			gKey:   r.Header.Get("x-goog-api-key"),
			body:   string(body),
		}
		last.Store(c)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, last
}

func startProxy(t *testing.T, overrides map[string]string) *Server {
	t.Helper()
	routes, err := BuildProviderRoutes(overrides)
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
	return srv
}

func TestProxyForwardsOpenAI(t *testing.T) {
	upstream, captured := startUpstream(t, `{"ok":true}`)
	srv := startProxy(t, map[string]string{"openai": upstream.URL})

	req, _ := http.NewRequest(http.MethodPost,
		"http://"+srv.Addr()+"/openai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o-mini"}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	c := captured.Load()
	if c == nil {
		t.Fatal("upstream not called")
	}
	if c.path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q", c.path)
	}
	if c.auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", c.auth)
	}
	if c.body != `{"model":"gpt-4o-mini"}` {
		t.Errorf("body = %q", c.body)
	}
}

func TestProxyForwardsAnthropicAuth(t *testing.T) {
	upstream, captured := startUpstream(t, `{}`)
	srv := startProxy(t, map[string]string{"anthropic": upstream.URL})

	req, _ := http.NewRequest(http.MethodPost,
		"http://"+srv.Addr()+"/anthropic/v1/messages",
		strings.NewReader(`{}`))
	req.Header.Set("x-api-key", "anthropic-key")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	c := captured.Load()
	if c == nil {
		t.Fatal("upstream not called")
	}
	if c.path != "/v1/messages" {
		t.Errorf("path = %q", c.path)
	}
	if c.apiKey != "anthropic-key" {
		t.Errorf("x-api-key = %q", c.apiKey)
	}
}

func TestProxyForwardsGeminiAuth(t *testing.T) {
	upstream, captured := startUpstream(t, `{}`)
	srv := startProxy(t, map[string]string{"gemini": upstream.URL})

	req, _ := http.NewRequest(http.MethodPost,
		"http://"+srv.Addr()+"/gemini/v1/models/gemini-1.5-pro:generateContent",
		strings.NewReader(`{}`))
	req.Header.Set("x-goog-api-key", "gemini-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	c := captured.Load()
	if c == nil {
		t.Fatal("upstream not called")
	}
	if c.path != "/v1/models/gemini-1.5-pro:generateContent" {
		t.Errorf("path = %q", c.path)
	}
	if c.gKey != "gemini-key" {
		t.Errorf("x-goog-api-key = %q", c.gKey)
	}
}

func TestProxyHonoursUpstreamPath(t *testing.T) {
	upstream, captured := startUpstream(t, `{}`)
	srv := startProxy(t, map[string]string{"openai": upstream.URL + "/proxy-root"})

	req, _ := http.NewRequest(http.MethodPost,
		"http://"+srv.Addr()+"/openai/v1/chat/completions",
		strings.NewReader(`{}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	c := captured.Load()
	if c == nil {
		t.Fatal("upstream not called")
	}
	if c.path != "/proxy-root/v1/chat/completions" {
		t.Errorf("expected upstream root preserved, got %q", c.path)
	}
}

func TestProxyUpstreamErrorReturns502(t *testing.T) {
	srv := startProxy(t, map[string]string{"openai": "http://127.0.0.1:1"})

	resp, err := http.Get("http://" + srv.Addr() + "/openai/v1/chat/completions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestSingleJoin(t *testing.T) {
	cases := []struct {
		base, tail, want string
	}{
		{"", "/v1", "/v1"},
		{"/", "/v1", "/v1"},
		{"/proxy", "/v1", "/proxy/v1"},
		{"/proxy/", "/v1", "/proxy/v1"},
		{"/proxy", "v1", "/proxy/v1"},
		{"/proxy/", "v1", "/proxy/v1"},
		{"", "v1", "/v1"},
	}
	for _, c := range cases {
		if got := singleJoin(c.base, c.tail); got != c.want {
			t.Errorf("singleJoin(%q,%q) = %q, want %q", c.base, c.tail, got, c.want)
		}
	}
}
