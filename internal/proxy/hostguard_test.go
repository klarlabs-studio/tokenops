package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostGuardAllowsHost(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		extra  []string
		host   string
		want   bool
	}{
		{"loopback v4 with port", "127.0.0.1:7878", nil, "127.0.0.1:7878", true},
		{"loopback v4 other port", "127.0.0.1:7878", nil, "127.0.0.1:9999", true},
		{"loopback v4 range", "127.0.0.1:7878", nil, "127.1.2.3:7878", true},
		{"loopback v6", "127.0.0.1:7878", nil, "[::1]:7878", true},
		{"loopback v6 bare", "127.0.0.1:7878", nil, "[::1]", true},
		{"localhost", "127.0.0.1:7878", nil, "localhost:7878", true},
		{"localhost upper and trailing dot", "127.0.0.1:7878", nil, "LOCALHOST.:7878", true},
		{"localhost subdomain", "127.0.0.1:7878", nil, "app.localhost:7878", true},
		{"no host header (HTTP/1.0 client)", "127.0.0.1:7878", nil, "", true},
		{"rebinding name", "127.0.0.1:7878", nil, "evil.example:7878", false},
		{"localhost lookalike", "127.0.0.1:7878", nil, "localhost.evil.example:7878", false},
		{"lan ip on loopback listen", "127.0.0.1:7878", nil, "192.168.1.5:7878", false},
		{"configured name", "127.0.0.1:7878", []string{"Tokenops.Lan"}, "tokenops.lan:7878", true},
		{"configured name with port", "127.0.0.1:7878", []string{"box.lan:7878"}, "box.lan", true},
		{"specific lan listen ip", "192.168.1.5:7878", nil, "192.168.1.5:7878", true},
		{"specific lan listen other ip", "192.168.1.5:7878", nil, "10.0.0.9:7878", false},
		{"listen host name", "box.lan:7878", nil, "box.lan:7878", true},
		{"wildcard listen any ip", "0.0.0.0:7878", nil, "10.0.0.9:7878", true},
		{"wildcard v6 listen any ip", "[::]:7878", nil, "[fe80::1]:7878", true},
		{"empty host listen any ip", ":7878", nil, "10.0.0.9:7878", true},
		{"wildcard listen still rejects names", "0.0.0.0:7878", nil, "evil.example:7878", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newHostGuard(tt.listen, tt.extra)
			if got := g.allowsHost(tt.host); got != tt.want {
				t.Errorf("allowsHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestHostGuardMiddleware(t *testing.T) {
	tests := []struct {
		name     string
		listen   string
		method   string
		path     string
		host     string
		headers  map[string]string
		wantCode int
	}{
		{"cli client no origin", "127.0.0.1:7878", "POST", "/anthropic/v1/messages", "127.0.0.1:7878", nil, http.StatusOK},
		{"rebinding host", "127.0.0.1:7878", "POST", "/anthropic/v1/messages", "evil.example:7878", nil, http.StatusForbidden},
		{"rebinding host on api", "127.0.0.1:7878", "GET", "/api/glance", "evil.example:7878", nil, http.StatusForbidden},
		{"cross-site simple post", "127.0.0.1:7878", "POST", "/anthropic/v1/messages", "127.0.0.1:7878",
			map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site", "Content-Type": "text/plain"}, http.StatusForbidden},
		{"foreign origin without fetch metadata", "127.0.0.1:7878", "POST", "/openai/v1/chat/completions", "127.0.0.1:7878",
			map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"null origin", "127.0.0.1:7878", "POST", "/openai/v1/chat/completions", "127.0.0.1:7878",
			map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"cross-site no-cors get without origin", "127.0.0.1:7878", "GET", "/openai/v1/models", "127.0.0.1:7878",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"loopback origin cross-site by port scheme", "127.0.0.1:7878", "POST", "/openai/v1/chat/completions", "127.0.0.1:7878",
			map[string]string{"Origin": "http://localhost:3000", "Sec-Fetch-Site": "cross-site"}, http.StatusOK},
		{"same-origin browser", "127.0.0.1:7878", "GET", "/api/glance", "127.0.0.1:7878",
			map[string]string{"Origin": "http://127.0.0.1:7878", "Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"typed url navigation", "127.0.0.1:7878", "GET", "/version", "127.0.0.1:7878",
			map[string]string{"Sec-Fetch-Site": "none"}, http.StatusOK},
		{"wildcard listen does not admit ip-literal origins", "0.0.0.0:7878", "POST", "/openai/v1/chat/completions", "10.0.0.9:7878",
			map[string]string{"Origin": "http://203.0.113.7"}, http.StatusForbidden},
		{"wildcard listen lan client", "0.0.0.0:7878", "POST", "/openai/v1/chat/completions", "10.0.0.9:7878", nil, http.StatusOK},
		{"healthz on any host", "127.0.0.1:7878", "GET", "/healthz", "kube-probe.internal:7878", nil, http.StatusOK},
		{"readyz on any host", "127.0.0.1:7878", "GET", "/readyz", "kube-probe.internal:7878", nil, http.StatusOK},
		{"version on any host", "127.0.0.1:7878", "GET", "/version", "kube-probe.internal:7878", nil, http.StatusOK},
		{"probe path post is not exempt", "127.0.0.1:7878", "POST", "/healthz", "evil.example:7878", nil, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			h := newHostGuard(tt.listen, nil).middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), next)
			req := httptest.NewRequest(tt.method, "http://placeholder"+tt.path, strings.NewReader("{}"))
			req.Host = tt.host
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if reached != (tt.wantCode == http.StatusOK) {
				t.Errorf("handler reached = %v, want %v", reached, tt.wantCode == http.StatusOK)
			}
		})
	}
}

// TestProxyRejectsCrossSiteRequestBeforeUpstream drives the real listener:
// a browser-shaped cross-site POST must neither reach the provider nor be
// observed, while the same request from a CLI client still goes through.
func TestProxyRejectsCrossSiteRequestBeforeUpstream(t *testing.T) {
	upstream, captured := startUpstream(t, `{"ok":true}`)
	srv := startProxy(t, map[string]string{"anthropic": upstream.URL})
	url := "http://" + srv.Addr() + "/anthropic/v1/messages"

	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"model":"claude"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", resp.StatusCode)
	}
	if captured.Load() != nil {
		t.Fatal("cross-site request reached the upstream provider")
	}

	rebind, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"model":"claude"}`))
	rebind.Host = "attacker.example:7878"
	resp, err = http.DefaultClient.Do(rebind)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebinding status = %d, want 403", resp.StatusCode)
	}
	if captured.Load() != nil {
		t.Fatal("rebinding request reached the upstream provider")
	}

	cli, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"model":"claude"}`))
	cli.Header.Set("x-api-key", "sk-test")
	resp, err = http.DefaultClient.Do(cli)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CLI status = %d, want 200", resp.StatusCode)
	}
	if captured.Load() == nil {
		t.Fatal("CLI request did not reach the upstream provider")
	}
}
