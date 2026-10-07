package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// fakeSource stands in for the HTTP client: it decodes body as the user
// record, or fails with err.
type fakeSource struct {
	body string
	err  error
}

func (f fakeSource) User(context.Context) (*UserResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	var u UserResponse
	if err := json.Unmarshal([]byte(f.body), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func sourceFor(f fakeSource) func(string) UserSource {
	return func(string) UserSource { return f }
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
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		OAuthToken: "tok",
		Interval:   time.Hour, // we drive scans manually
		NewClient:  sourceFor(fakeSource{body: sampleResponse}),
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
	p := NewPoller(nil, PollerOptions{
		OAuthToken: "bad",
		NewClient:  sourceFor(fakeSource{err: errors.New("copilot user: status 401: ")}),
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

func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
