package claudecodejsonl

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
func (b *captureBus) DroppedCount() int64         { return 0 }
func (b *captureBus) Close(_ time.Duration) error { return nil }

const sampleTurn = `{"type":"assistant","timestamp":"2026-05-14T09:22:45.151Z","sessionId":"s1","message":{"id":"msg_a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50}}}`

// First scan publishes one envelope per (file, assistant turn). A
// second scan against the same files must emit zero — the in-memory
// seen-set dedups by Anthropic message ID.
func TestPollerDedupesAcrossScans(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "tokenops-projects")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "sess.jsonl"), []byte(sampleTurn+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 1 {
		t.Fatalf("first scan want 1 envelope; got %d", got)
	}
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 1 {
		t.Errorf("second scan dedupe failed; total %d", got)
	}
}

// Concurrent sessions writing the same message ID still emit a single
// envelope — operators running multiple Claude Code instances must
// not double-count.
func TestPollerMergesConcurrentSessions(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two session files, same message ID in both (would happen if
	// Claude Code race-writes during session switch).
	for _, n := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(proj, n), []byte(sampleTurn+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 1 {
		t.Errorf("want 1 envelope across two files with same message ID; got %d", got)
	}
}

// newEnvelope must roll cache_read + cache_creation into InputTokens
// so spend.Engine attaches a price. Source tag pinned for
// signal_quality. Deterministic ID per message — restart-safe.
func TestNewEnvelopeShape(t *testing.T) {
	turn := Turn{
		Timestamp:                time.Date(2026, 5, 14, 9, 22, 45, 0, time.UTC),
		SessionID:                "s1",
		MessageID:                "msg_a",
		Model:                    "claude-opus-4-7",
		InputTokens:              10,
		OutputTokens:             20,
		CacheReadInputTokens:     1000,
		CacheCreationInputTokens: 50,
	}
	env := newEnvelope(turn)
	if env.Source != SourceTag {
		t.Errorf("Source = %q; want %q", env.Source, SourceTag)
	}
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderAnthropic {
		t.Errorf("Provider = %s", pe.Provider)
	}
	// 10 + 1000 + 50 = 1060 input, 20 output.
	if pe.InputTokens != 1060 || pe.OutputTokens != 20 {
		t.Errorf("token roll-up: in=%d out=%d", pe.InputTokens, pe.OutputTokens)
	}
	if pe.TotalTokens != 1080 {
		t.Errorf("total = %d; want 1080", pe.TotalTokens)
	}
	// Re-mint with identical inputs → identical ID (dedup-safe).
	env2 := newEnvelope(turn)
	if env.ID != env2.ID {
		t.Errorf("envelope IDs must be deterministic per message: %s vs %s", env.ID, env2.ID)
	}
}
