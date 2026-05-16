package copilot

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

const sampleResponse = `{
  "login": "test-user",
  "chat_enabled": true,
  "quota_reset_date": "2026-06-01",
  "timestamp_utc": "2026-05-16T07:50:00Z",
  "quota_snapshots": {
    "chat": {"entitlement":300,"remaining":210.5,"percent_remaining":70.0,"overage_count":0,"unlimited":false},
    "premium_interactions": {"entitlement":50,"remaining":50,"percent_remaining":100.0,"unlimited":false}
  }
}`

// LoadToken must walk the candidate paths in order, skip missing files,
// and return the first non-empty oauth_token. Errors short-circuit
// only when no token is anywhere.
func TestLoadTokenWalksPaths(t *testing.T) {
	dir := t.TempDir()
	apps := filepath.Join(dir, "apps.json")
	if err := os.WriteFile(apps, []byte(`{"app1":{"user":"u","oauth_token":"tok-xyz","githubAppId":"a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadToken([]string{filepath.Join(dir, "missing.json"), apps})
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "tok-xyz" {
		t.Errorf("got %q; want tok-xyz", tok)
	}
}

// All paths missing → ErrNoToken so callers can detect "not signed
// in" vs a parse failure.
func TestLoadTokenReturnsErrNoToken(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadToken([]string{filepath.Join(dir, "nope.json")})
	if err == nil {
		t.Fatal("want error")
	}
	if err != ErrNoToken {
		t.Errorf("want ErrNoToken; got %v", err)
	}
}

// Client.User must send the Authorization header in token form, hit
// the right path, and decode the documented response shape.
func TestClientUserHappyPath(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()
	c := NewClient("tok-abc")
	c.BaseURL = srv.URL
	resp, err := c.User(context.Background())
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if gotAuth != "token tok-abc" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotPath != "/copilot_internal/user" {
		t.Errorf("path = %q", gotPath)
	}
	if resp.Login != "test-user" {
		t.Errorf("login = %q", resp.Login)
	}
	chat, ok := resp.QuotaSnapshots["chat"]
	if !ok {
		t.Fatal("chat snapshot missing")
	}
	if chat.PercentRemaining != 70.0 {
		t.Errorf("chat percent_remaining = %v", chat.PercentRemaining)
	}
}

// Empty token short-circuits with ErrNoToken before any HTTP call.
func TestClientUserMissingToken(t *testing.T) {
	c := &Client{}
	_, err := c.User(context.Background())
	if err != ErrNoToken {
		t.Errorf("want ErrNoToken; got %v", err)
	}
}

// Non-2xx response includes status + body snippet so operators can
// diagnose auth failure / rate-limit.
func TestClientUserNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"message":"bad creds"}`))
	}))
	defer srv.Close()
	c := NewClient("bad")
	c.BaseURL = srv.URL
	_, err := c.User(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
}

type captureBus struct {
	mu        sync.Mutex
	envelopes []*eventschema.Envelope
}

func (b *captureBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.envelopes = append(b.envelopes, env)
}
func (b *captureBus) PublishedCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(len(b.envelopes))
}
func (b *captureBus) DroppedCount() int64         { return 0 }
func (b *captureBus) Close(_ time.Duration) error { return nil }

// Each quota_snapshots key yields one envelope. Re-running the same
// poll within the same response timestamp must be a no-op (server
// data hasn't changed yet).
func TestPollerEmitsOneEnvelopePerSnapshotAndDedupes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		OAuthToken: "tok",
		Interval:   time.Hour, // we drive scans manually
		BaseURL:    srv.URL,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 2 {
		t.Fatalf("want 2 envelopes (one per snapshot); got %d", got)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 2 {
		t.Errorf("same timestamp_utc must dedupe; total %d", got)
	}
}

// 401 from the API records LastError so the CLI status command can
// surface it.
func TestPollerRecordsLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	p := NewPoller(nil, PollerOptions{
		OAuthToken: "bad",
		BaseURL:    srv.URL,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	p.scan(context.Background())
	if _, err := p.LastError(); err == nil {
		t.Fatal("expected LastError after 401")
	}
}
