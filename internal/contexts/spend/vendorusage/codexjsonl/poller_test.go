package codexjsonl

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

const codexFile = `{"timestamp":"2026-04-17T08:44:43.195Z","type":"session_meta","payload":{"id":"s1"}}
{"timestamp":"2026-04-17T08:45:14.725Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":50,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":175}},"rate_limits":{"primary":{"used_percent":10.0,"window_minutes":300,"resets_at":1776431887},"secondary":{"used_percent":2.0,"window_minutes":10080,"resets_at":1777018687},"plan_type":"plus"}}}
`

// First scan emits one envelope per token_count row; second scan
// dedupes by (sessionID, sequence) so no double-count.
func TestPollerDedupesAcrossScans(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "2026", "04", "17")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "rollout-x.jsonl"), []byte(codexFile), 0o644); err != nil {
		t.Fatal(err)
	}
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 1 {
		t.Fatalf("first scan want 1; got %d", got)
	}
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 1 {
		t.Errorf("dedup failed; total %d", got)
	}
}

// newEnvelope encodes the rate_limits block into Attributes so the
// downstream signal_quality classifier and a future session_budget
// implementation can read it without re-parsing the source file.
func TestNewEnvelopeCarriesRateLimitsInAttributes(t *testing.T) {
	turn := Turn{
		Timestamp:    time.Date(2026, 4, 17, 8, 45, 14, 725_000_000, time.UTC),
		SessionID:    "s1",
		InputTokens:  100,
		OutputTokens: 20,
		ReasoningTok: 5,
		TotalTokens:  125,
		RateLimits: RateLimits{
			PrimaryUsedPercent:     10.0,
			PrimaryWindowMinutes:   300,
			PrimaryResetsAtUnix:    1776431887,
			SecondaryUsedPercent:   2.0,
			SecondaryWindowMinutes: 10080,
			SecondaryResetsAtUnix:  1777018687,
			PlanType:               "plus",
		},
		RecordSequence: 1,
	}
	env := newEnvelope(turn)
	if env.Source != SourceTag {
		t.Errorf("source = %q", env.Source)
	}
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderOpenAI {
		t.Errorf("provider = %s", pe.Provider)
	}
	if pe.OutputTokens != 25 {
		t.Errorf("OutputTokens should sum output + reasoning (20+5=25); got %d", pe.OutputTokens)
	}
	if env.Attributes["plan_type"] != "plus" {
		t.Errorf("plan_type attr missing")
	}
	if env.Attributes["primary_used_pct"] != "10.00" {
		t.Errorf("primary_used_pct attr = %q", env.Attributes["primary_used_pct"])
	}
	if env.Attributes["secondary_used_pct"] != "2.00" {
		t.Errorf("secondary_used_pct attr = %q", env.Attributes["secondary_used_pct"])
	}
}
