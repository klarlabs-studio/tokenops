package cursor

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

const sampleResponse = `{
  "gpt-4": {"numRequests": 120, "maxRequestUsage": 500},
  "gpt-4-32k": {"numRequests": 12, "maxRequestUsage": 50},
  "premiumRequests": {"numRequests": 0, "maxRequestUsage": 0},
  "startOfMonth": "2026-05-01T00:00:00.000Z"
}`

// Client.Usage sends WorkosCursorSessionToken in the Cookie header,
// uses ?user= query, and decodes the per-model map.
func TestClientUsageHappyPath(t *testing.T) {
	var gotCookie, gotUser, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotUser = r.URL.Query().Get("user")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()

	c := NewClient("tok-xyz", "user-123")
	c.BaseURL = srv.URL
	resp, err := c.Usage(context.Background())
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if gotCookie != "WorkosCursorSessionToken=tok-xyz" {
		t.Errorf("cookie header = %q", gotCookie)
	}
	if gotUser != "user-123" {
		t.Errorf("user query = %q", gotUser)
	}
	if gotPath != "/api/usage" {
		t.Errorf("path = %q", gotPath)
	}
	if len(resp.Models) != 3 {
		t.Fatalf("want 3 model rows; got %d", len(resp.Models))
	}
	if resp.Models["gpt-4"].NumRequests != 120 {
		t.Errorf("gpt-4 numRequests = %d", resp.Models["gpt-4"].NumRequests)
	}
	if resp.StartOfMonth != "2026-05-01T00:00:00.000Z" {
		t.Errorf("startOfMonth = %q", resp.StartOfMonth)
	}
}

// Either Cookie or UserID empty → ErrMissingCredential, no HTTP call.
func TestClientUsageMissingCredentials(t *testing.T) {
	if _, err := (&Client{Cookie: "x"}).Usage(context.Background()); err != ErrMissingCredential {
		t.Errorf("want ErrMissingCredential when UserID empty; got %v", err)
	}
	if _, err := (&Client{UserID: "u"}).Usage(context.Background()); err != ErrMissingCredential {
		t.Errorf("want ErrMissingCredential when Cookie empty; got %v", err)
	}
}

// Non-2xx surfaces status + body snippet.
func TestClientUsageNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"expired cookie"}`))
	}))
	defer srv.Close()
	c := NewClient("bad", "u")
	c.BaseURL = srv.URL
	if _, err := c.Usage(context.Background()); err == nil {
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

// Each model row yields one envelope. Same-content second scan must
// dedupe via snapshotKey.
func TestPollerEmitsOneEnvelopePerModelAndDedupes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Cookie:   "tok",
		UserID:   "u",
		Interval: time.Hour,
		BaseURL:  srv.URL,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 3 {
		t.Fatalf("want 3 envelopes (one per model row); got %d", got)
	}
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 3 {
		t.Errorf("same snapshot must dedupe; total %d", got)
	}
}

// newEnvelope encodes the per-model snapshot + used_pct in Attributes
// and tags with ProviderCursor.
func TestNewEnvelopeAttrShape(t *testing.T) {
	env := newEnvelope(
		time.Now().UTC(),
		"user-1",
		"2026-05-01T00:00:00Z",
		"gpt-4",
		ModelUsage{NumRequests: 250, MaxRequestUsage: 500},
	)
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderCursor {
		t.Errorf("provider = %s", pe.Provider)
	}
	if env.Source != SourceTag {
		t.Errorf("source = %q", env.Source)
	}
	if env.Attributes["used_pct"] != "50.00" {
		t.Errorf("used_pct = %q", env.Attributes["used_pct"])
	}
	if env.Attributes["num_requests"] != "250" {
		t.Errorf("num_requests = %q", env.Attributes["num_requests"])
	}
}
