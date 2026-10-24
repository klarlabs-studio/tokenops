package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func earlyLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func earlyGet(t *testing.T, url string) (int, http.Header, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, resp.Header, body
}

// Before the server takes the listener over, the probes answer at once
// and say the daemon is starting, and the API says so rather than
// waiting: `daemon restart` sees a live daemon, and a CLI call can tell
// "starting" from "down".
func TestEarlyListenerAnswersWhileStarting(t *testing.T) {
	e, err := ListenEarly("127.0.0.1:0", nil, nil, earlyLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	base := "http://" + e.Addr()

	code, _, body := earlyGet(t, base+"/healthz")
	if code != http.StatusOK || body["status"] != "ok" || body["state"] != "starting" {
		t.Errorf("/healthz = %d %v, want 200 ok/starting", code, body)
	}
	code, _, body = earlyGet(t, base+"/readyz")
	if code != http.StatusServiceUnavailable || body["status"] != "starting" {
		t.Errorf("/readyz = %d %v, want 503 starting", code, body)
	}
	code, _, body = earlyGet(t, base+"/version")
	if code != http.StatusOK || body["version"] == nil {
		t.Errorf("/version = %d %v, want 200 with the version", code, body)
	}
	start := time.Now()
	code, hdr, body := earlyGet(t, base+"/api/spend/summary")
	if code != http.StatusServiceUnavailable || body["error"] != "starting" || hdr.Get("Retry-After") == "" {
		t.Errorf("/api/spend/summary = %d %v (Retry-After %q), want 503 starting with Retry-After", code, body, hdr.Get("Retry-After"))
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("the API waited %s for the boot; it should answer at once", took)
	}
}

// A foreign Host is refused while starting as it is afterwards; the
// probes answer whatever the Host.
func TestEarlyListenerKeepsTheHostGuard(t *testing.T) {
	e, err := ListenEarly("127.0.0.1:0", nil, nil, earlyLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	req, _ := http.NewRequest(http.MethodGet, "http://"+e.Addr()+"/api/spend/summary", nil)
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host while starting = %d, want 403", resp.StatusCode)
	}
}

// A provider request that arrives during the boot waits for the proxy
// and is then served by it, on the same listener, without the client
// seeing a refused connection or a 503.
func TestServerTakesOverTheEarlyListener(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	anthropic, _ := providers.Lookup(eventschema.ProviderAnthropic)

	e, err := ListenEarly("127.0.0.1:0", nil, nil, earlyLogger())
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + e.Addr()

	type result struct {
		code int
		body string
		err  error
	}
	held := make(chan result, 1)
	go func() {
		resp, err := http.Post(base+"/anthropic/v1/messages", "application/json",
			strings.NewReader(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`))
		if err != nil {
			held <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		held <- result{code: resp.StatusCode, body: string(b)}
	}()
	select {
	case r := <-held:
		t.Fatalf("a provider request was answered before the proxy existed: %+v", r)
	case <-time.After(200 * time.Millisecond):
	}

	srv := New("127.0.0.1:0",
		WithLogger(earlyLogger()),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{{Provider: anthropic, Upstream: u}}),
		WithEarlyListener(e),
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		_ = srv.Shutdown(c)
	})
	if srv.Addr() != e.Addr() {
		t.Errorf("server address %s, want the early listener's %s", srv.Addr(), e.Addr())
	}

	select {
	case r := <-held:
		if r.err != nil || r.code != http.StatusOK || !strings.Contains(r.body, `"ok"`) {
			t.Errorf("held request = %+v, want the upstream's 200", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held request was never served")
	}
	code, _, body := earlyGet(t, base+"/healthz")
	if code != http.StatusOK || body["state"] != nil {
		t.Errorf("/healthz after the hand-over = %d %v, want the server's own answer", code, body)
	}
	// Close after the hand-over leaves the server serving.
	_ = e.Close()
	if code, _, _ := earlyGet(t, base+"/healthz"); code != http.StatusOK {
		t.Errorf("/healthz after Close on a handed-over listener = %d, want 200", code)
	}
}

// A boot that fails before the hand-over releases the address.
func TestEarlyListenerCloseReleasesTheAddress(t *testing.T) {
	e, err := ListenEarly("127.0.0.1:0", nil, nil, earlyLogger())
	if err != nil {
		t.Fatal(err)
	}
	addr := e.Addr()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("address still held after Close: %v", err)
	}
	_ = ln.Close()
}
