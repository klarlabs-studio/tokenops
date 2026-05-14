package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// captureBus records every envelope so the test asserts on the
// poller's output without standing up a real events.AsyncBus.
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

// First scan must publish one envelope per (date, model) in the
// cache. A second scan against the same cache contents must publish
// nothing — the diff state is keyed by cumulative tokens.
func TestPollerEmitsDeltasOncePerCacheState(t *testing.T) {
	path := writeCache(t, map[string]int64{
		"claude-opus-4-7":         1_000_000,
		"claude-haiku-4-5-202510": 200_000,
	})
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Path:   path,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx := context.Background()
	p.scan(ctx, path)
	if got := bus.PublishedCount(); got != 2 {
		t.Fatalf("first scan want 2 envelopes; got %d", got)
	}
	p.scan(ctx, path)
	if got := bus.PublishedCount(); got != 2 {
		t.Errorf("second scan should be a no-op; total now %d", got)
	}
}

// When the cache grows for an already-seen (date, model), the poller
// emits an envelope whose TotalTokens equals the delta (not the new
// cumulative total).
func TestPollerEmitsOnlyDeltaForGrowingModel(t *testing.T) {
	path := writeCache(t, map[string]int64{"claude-opus-4-7": 1_000})
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Path: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background(), path)
	// Grow the cache.
	overwriteCache(t, path, map[string]int64{"claude-opus-4-7": 3_500})
	p.scan(context.Background(), path)
	if got := bus.PublishedCount(); got != 2 {
		t.Fatalf("want 2 envelopes total; got %d", got)
	}
	last := bus.envelopes[1].Payload.(*eventschema.PromptEvent)
	if last.TotalTokens != 2_500 {
		t.Errorf("delta envelope TotalTokens = %d; want 2500", last.TotalTokens)
	}
}

// Source tag is the upgrade contract — signal_quality looks for it
// when promoting Anthropic confidence above "low". Lock it down.
func TestEnvelopeSourceTag(t *testing.T) {
	env, ok := newEnvelope("2026-05-01", "claude-opus-4-7", 1000, Model{InputTokens: 100, OutputTokens: 900})
	if !ok {
		t.Fatal("newEnvelope returned !ok")
	}
	if env.Source != SourceTag {
		t.Errorf("source = %q; want %q", env.Source, SourceTag)
	}
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderAnthropic {
		t.Errorf("provider = %s", pe.Provider)
	}
	// 10% input / 90% output split per the cumulative ratio.
	if pe.InputTokens != 100 || pe.OutputTokens != 900 {
		t.Errorf("split: in=%d out=%d; want 100/900", pe.InputTokens, pe.OutputTokens)
	}
}

// Empty cumulative summary triggers the 1:99 fallback split so we
// never crash on a fresh install.
func TestSplitDeltaFallback(t *testing.T) {
	in, out := splitDelta(1000, Model{})
	if in+out != 1000 {
		t.Errorf("split must sum to delta: %d+%d", in, out)
	}
	if in >= out {
		t.Errorf("fallback expects output-heavy split: %d/%d", in, out)
	}
}

// Snapshot reads the live cache once. Useful for the CLI status
// command; the only thing to assert is that it returns the parsed
// cache without invoking the bus.
func TestSnapshot(t *testing.T) {
	path := writeCache(t, map[string]int64{"claude-opus-4-7": 5_000})
	p := NewPoller(nil, PollerOptions{Path: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	c, err := p.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.TokensForDate(today())["claude-opus-4-7"]; got != 5_000 {
		t.Errorf("snapshot token count = %d", got)
	}
}

func writeCache(t *testing.T, tokens map[string]int64) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stats-cache.json")
	cache := StatsCache{
		Version: 3,
		DailyModelTokens: []DailyModelTokens{
			{Date: today(), TokensByModel: tokens},
		},
	}
	data, _ := json.MarshalIndent(cache, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func overwriteCache(t *testing.T, path string, tokens map[string]int64) {
	t.Helper()
	cache := StatsCache{
		Version: 3,
		DailyModelTokens: []DailyModelTokens{
			{Date: today(), TokensByModel: tokens},
		},
	}
	data, _ := json.MarshalIndent(cache, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func today() string { return time.Now().UTC().Format("2006-01-02") }
