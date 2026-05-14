package anthropic

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
func (b *captureBus) DroppedCount() int64 { return 0 }
func (b *captureBus) Close(time.Duration) error {
	return nil
}

// Each non-zero (bucket, model) cell produces one envelope tagged with
// the SourceTag so signal_quality can upgrade Anthropic confidence.
func TestPollerEmitsOneEnvelopePerResult(t *testing.T) {
	body := `{
		"data": [{
			"starting_at": "2026-05-14T00:00:00Z",
			"ending_at":   "2026-05-14T01:00:00Z",
			"results": [
				{"uncached_input_tokens": 1000, "cache_read_input_tokens": 0, "cache_creation": {}, "output_tokens": 200, "server_tool_use": {}, "model": "claude-opus-4-7"},
				{"uncached_input_tokens": 500,  "cache_read_input_tokens": 0, "cache_creation": {}, "output_tokens": 100, "server_tool_use": {}, "model": "claude-haiku-4-5"}
			]
		}],
		"has_more": false
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	bus := &captureBus{}
	client := NewAdminClient("sk-ant-admin-test")
	client.BaseURL = srv.URL
	p := NewPoller(client, bus, PollerOptions{
		AdminKey: "sk-ant-admin-test",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 2 {
		t.Fatalf("want 2 envelopes; got %d", got)
	}
	for _, env := range bus.envelopes {
		if env.Source != SourceTag {
			t.Errorf("source = %q; want %q", env.Source, SourceTag)
		}
		if env.Payload.(*eventschema.PromptEvent).Provider != eventschema.ProviderAnthropic {
			t.Errorf("provider != anthropic")
		}
	}
}

// Empty result rows must produce zero envelopes so a quiet bucket
// doesn't flood the store.
func TestPollerSkipsZeroTokenResults(t *testing.T) {
	body := `{
		"data": [{
			"starting_at": "2026-05-14T00:00:00Z",
			"ending_at":   "2026-05-14T01:00:00Z",
			"results": [
				{"uncached_input_tokens": 0, "cache_read_input_tokens": 0, "cache_creation": {}, "output_tokens": 0, "server_tool_use": {}, "model": "claude-opus-4-7"}
			]
		}],
		"has_more": false
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	bus := &captureBus{}
	client := NewAdminClient("k")
	client.BaseURL = srv.URL
	p := NewPoller(client, bus, PollerOptions{AdminKey: "k", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background())
	if got := bus.PublishedCount(); got != 0 {
		t.Errorf("expected zero envelopes for zero-token bucket; got %d", got)
	}
}

// Errors are recorded on the poller (visible via LastError) so the
// CLI status command can show the operator why no data is flowing.
func TestPollerRecordsLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	client := NewAdminClient("bad-key")
	client.BaseURL = srv.URL
	p := NewPoller(client, nil, PollerOptions{AdminKey: "bad-key", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background())
	when, err := p.LastError()
	if err == nil {
		t.Fatal("LastError should be populated after 401")
	}
	if when.IsZero() {
		t.Errorf("LastError timestamp should be set")
	}
}

// Successful poll after an error clears the error state so the
// status command can report current health, not just past failures.
func TestPollerClearsLastErrorOnSuccess(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [], "has_more": false}`))
	}))
	defer srv.Close()
	client := NewAdminClient("k")
	client.BaseURL = srv.URL
	p := NewPoller(client, nil, PollerOptions{AdminKey: "k", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background())
	if _, err := p.LastError(); err == nil {
		t.Fatal("error should be set after first scan")
	}
	fail = false
	p.scan(context.Background())
	if _, err := p.LastError(); err != nil {
		t.Errorf("LastError should clear on success; still %v", err)
	}
}
