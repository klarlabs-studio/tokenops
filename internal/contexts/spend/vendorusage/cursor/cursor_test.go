package cursor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

const sampleResponse = `{
  "gpt-4": {"numRequests": 120, "maxRequestUsage": 500},
  "gpt-4-32k": {"numRequests": 12, "maxRequestUsage": 50},
  "premiumRequests": {"numRequests": 0, "maxRequestUsage": 0},
  "startOfMonth": "2026-05-01T00:00:00.000Z"
}`

// fakeSource stands in for the HTTP client: it decodes body as the
// usage snapshot.
type fakeSource struct{ body string }

func (f fakeSource) Usage(context.Context) (*UsageResponse, error) {
	var u UsageResponse
	if err := json.Unmarshal([]byte(f.body), &u); err != nil {
		return nil, err
	}
	return &u, nil
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
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Cookie:   "tok",
		UserID:   "u",
		Interval: time.Hour,
		NewClient: func(string, string) UsageSource {
			return fakeSource{body: sampleResponse}
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
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

func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
