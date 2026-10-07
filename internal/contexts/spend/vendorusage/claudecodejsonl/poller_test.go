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

	"go.klarlabs.de/tokenops/pkg/eventschema"
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
		Timestamp:                  time.Date(2026, 5, 14, 9, 22, 45, 0, time.UTC),
		SessionID:                  "s1",
		MessageID:                  "msg_a",
		Model:                      "claude-opus-4-7",
		InputTokens:                10,
		OutputTokens:               20,
		CacheReadInputTokens:       1000,
		CacheCreationInputTokens:   50,
		CacheCreation1hInputTokens: 20,
	}
	env := newEnvelope(turn, "", "")
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
	// The cache read and the writes are portions of the input, each billed
	// at its own rate.
	if pe.CachedInputTokens != 1000 || pe.CacheWriteInputTokens != 50 || pe.CacheWrite1hInputTokens != 20 {
		t.Errorf("cache split: read=%d write=%d write1h=%d; want 1000/50/20",
			pe.CachedInputTokens, pe.CacheWriteInputTokens, pe.CacheWrite1hInputTokens)
	}
	// Re-mint with identical inputs → identical ID (dedup-safe).
	env2 := newEnvelope(turn, "", "")
	if env.ID != env2.ID {
		t.Errorf("envelope IDs must be deterministic per message: %s vs %s", env.ID, env2.ID)
	}
}

// SessionID + WorkflowID must land on the PromptEvent so `tokenops
// replay` and the waste detector can resolve a Claude Code session.
// Cache reads must also surface as CachedInputTokens for the
// cost-pricing path to discount them ($1.50/M vs $15/M).
func TestNewEnvelopeAttribution(t *testing.T) {
	turn := Turn{
		Timestamp:            time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		SessionID:            "abc-123",
		MessageID:            "msg_attribution",
		Model:                "claude-opus-4-7",
		InputTokens:          5_000,
		OutputTokens:         500,
		CacheReadInputTokens: 95_000,
	}
	env := newEnvelope(turn, "", "")
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.SessionID != "abc-123" {
		t.Errorf("SessionID = %q; want abc-123", pe.SessionID)
	}
	if pe.WorkflowID != "claude-code:abc-123" {
		t.Errorf("WorkflowID = %q; want claude-code:abc-123", pe.WorkflowID)
	}
	if pe.CachedInputTokens != 95_000 {
		t.Errorf("CachedInputTokens = %d; want 95000", pe.CachedInputTokens)
	}
}

// Plan-bound deployments stamp every JSONL event plan_included so the
// analytics recompute never reprices subscription-covered usage.
func TestNewEnvelopeStampsCostSource(t *testing.T) {
	turn := Turn{
		Timestamp: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		SessionID: "s1", MessageID: "m1", Model: "claude-fable-5",
		InputTokens: 10, OutputTokens: 5,
	}
	env := newEnvelope(turn, eventschema.CostSourcePlanIncluded, "")
	pe := env.Payload.(*eventschema.PromptEvent)
	if pe.CostSource != eventschema.CostSourcePlanIncluded {
		t.Errorf("CostSource = %q; want plan_included", pe.CostSource)
	}
	if pe := newEnvelope(turn, "", "").Payload.(*eventschema.PromptEvent); pe.CostSource != "" {
		t.Errorf("metered CostSource = %q; want empty", pe.CostSource)
	}
}

// A turn a gateway served is billed by the gateway and never covered by
// the Anthropic plan: on a Max plan with FireConnect, kimi-k3 turns were
// recorded as Anthropic's at $0.
func TestGatewayTurnIsBilledByTheGateway(t *testing.T) {
	base := Turn{Timestamp: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), SessionID: "s1", MessageID: "m1", InputTokens: 10, OutputTokens: 5}
	const fireworks = "https://api.fireworks.ai/inference"

	kimi := base
	kimi.Model = "kimi-k3"
	pe := newEnvelope(kimi, eventschema.CostSourcePlanIncluded, fireworks).Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderFireworks || pe.CostSource != eventschema.CostSourceMetered {
		t.Errorf("kimi-k3 via Fireworks: provider %q, cost source %q; want fireworks, billed", pe.Provider, pe.CostSource)
	}

	// FireRouter runs Claude on the Anthropic API key it is given, which
	// Anthropic bills per token: Anthropic's turn, not the plan's.
	claude := base
	claude.Model = "claude-opus-5-5"
	env := newEnvelope(claude, eventschema.CostSourcePlanIncluded, fireworks)
	pe = env.Payload.(*eventschema.PromptEvent)
	if pe.Provider != eventschema.ProviderAnthropic || pe.CostSource != eventschema.CostSourceMetered || env.Attributes["endpoint"] != "fireworks" {
		t.Errorf("Claude via FireRouter: provider %q, cost source %q, endpoint %q; want anthropic, billed, fireworks",
			pe.Provider, pe.CostSource, env.Attributes["endpoint"])
	}
	// Through Anthropic's own endpoint the plan covers it.
	if pe := newEnvelope(claude, eventschema.CostSourcePlanIncluded, "").Payload.(*eventschema.PromptEvent); pe.CostSource != eventschema.CostSourcePlanIncluded {
		t.Errorf("Claude direct: cost source %q, want plan_included", pe.CostSource)
	}
}

func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
