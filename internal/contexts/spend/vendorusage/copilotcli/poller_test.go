package copilotcli

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type recordingBus struct {
	mu  sync.Mutex
	got []*eventschema.Envelope
}

func (b *recordingBus) Publish(e *eventschema.Envelope) { _ = b.PublishWait(context.Background(), e) }
func (b *recordingBus) PublishWait(_ context.Context, e *eventschema.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, e)
	return nil
}
func (b *recordingBus) DroppedCount() int64       { return 0 }
func (b *recordingBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *recordingBus) Close(time.Duration) error { return nil }

// The poller emits each shutdown's usage once, however often it scans,
// and the session that ends later adds what it grew.
func TestPollerEmitsEachUsageOnce(t *testing.T) {
	root := t.TempDir()
	first := shutdownLine("e5", "2026-11-10T09:30:00Z", `{"m":{"usage":{"inputTokens":1000,"outputTokens":100,"cacheReadTokens":600,"cacheWriteTokens":0}}}`)
	path := writeSession(t, root, "s1", false, startLine, first)
	bus := &recordingBus{}
	p := NewPoller(bus, PollerOptions{CostSource: eventschema.CostSourcePlanIncluded})
	p.Scan(context.Background(), root)
	p.Scan(context.Background(), root)
	if len(bus.got) != 1 {
		t.Fatalf("published %d", len(bus.got))
	}
	writeSession(t, root, "s1", false, startLine, first,
		shutdownLine("e8", "2026-11-10T10:30:00Z", `{"m":{"usage":{"inputTokens":1500,"outputTokens":150,"cacheReadTokens":600,"cacheWriteTokens":0}}}`))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	p.Scan(context.Background(), root)
	if len(bus.got) != 2 {
		t.Fatalf("after the resumed session ended: published %d", len(bus.got))
	}
	if p := bus.got[1].Payload.(*eventschema.PromptEvent); p.InputTokens != 500 || p.OutputTokens != 50 {
		t.Errorf("growth %+v", p)
	}
}

// A usage maps to a Copilot PromptEvent for the session and model, its
// input counting the cache as Copilot CLI does, and marked as a session
// aggregate rather than a turn.
func TestEnvelope(t *testing.T) {
	u := Usage{ID: "e9|claude-sonnet-4.5", SessionID: "s1", Project: "shop", Model: "claude-sonnet-4.5",
		Requests: 12, Input: 50000, CacheRead: 40000, CacheWrite: 2000, Output: 3000, Reasoning: 100,
		Timestamp: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	env := newEnvelope(u, eventschema.CostSourcePlanIncluded)
	p, ok := env.Payload.(*eventschema.PromptEvent)
	if !ok {
		t.Fatalf("payload %T", env.Payload)
	}
	if env.Source != SourceTag || env.Type != eventschema.EventTypePrompt || !env.Timestamp.Equal(u.Timestamp) {
		t.Errorf("envelope %+v", env)
	}
	if env.Attributes["granularity"] != Granularity || env.Attributes["session_id"] != "s1" ||
		env.Attributes["requests"] != "12" || env.Attributes["reasoning_output"] != "100" {
		t.Errorf("attributes %v", env.Attributes)
	}
	if p.Provider != eventschema.ProviderGitHub || p.RequestModel != "claude-sonnet-4.5" ||
		p.InputTokens != 50000 || p.CachedInputTokens != 40000 || p.CacheWriteInputTokens != 2000 ||
		p.OutputTokens != 3000 || p.TotalTokens != 53000 || p.SessionID != "s1" ||
		p.AgentID != "copilot-cli:shop" || p.WorkflowID != "copilot-cli:shop:s1" ||
		p.CostSource != eventschema.CostSourcePlanIncluded {
		t.Errorf("payload %+v", p)
	}
	if again := newEnvelope(u, ""); again.ID != env.ID {
		t.Errorf("id not stable: %s vs %s", again.ID, env.ID)
	}
}
