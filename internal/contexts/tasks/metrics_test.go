package tasks

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// MetricsFor sums prompt events that fall within the task window.
// Events before / after the window are excluded. TTFUO is the
// delta from task start to the first prompt timestamp.
func TestMetricsFor(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	start := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)

	// Three prompts: one before window (exclude), two inside.
	events := []*eventschema.Envelope{
		{
			ID: "before", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: start.Add(-time.Minute), Source: "test",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-7",
				InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CostUSD: 0.1,
			},
		},
		{
			ID: "in1", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: start.Add(30 * time.Second), Source: "test",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-7",
				InputTokens: 500, OutputTokens: 50, TotalTokens: 550, CostUSD: 0.5,
			},
		},
		{
			ID: "in2", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: start.Add(2 * time.Minute), Source: "test",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-7",
				InputTokens: 700, OutputTokens: 70, TotalTokens: 770, CostUSD: 0.7,
			},
		},
	}
	if err := store.AppendBatch(ctx, events); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	task := Task{
		ID:          "t1",
		Description: "test",
		StartedAt:   start,
		CompletedAt: start.Add(3 * time.Minute),
	}
	m, err := MetricsFor(ctx, store, task, nil)
	if err != nil {
		t.Fatalf("MetricsFor: %v", err)
	}
	if m.Turns != 2 {
		t.Errorf("turns = %d; want 2 (one excluded by window)", m.Turns)
	}
	if m.InputTokens != 1200 {
		t.Errorf("input = %d; want 1200", m.InputTokens)
	}
	if m.CostUSD < 1.19 || m.CostUSD > 1.21 {
		t.Errorf("cost = %.2f; want ~1.20", m.CostUSD)
	}
	// TTFUO = 30 seconds (first in-window prompt offset from start)
	if m.TTFUOSeconds < 29 || m.TTFUOSeconds > 31 {
		t.Errorf("TTFUO = %.1f; want ~30", m.TTFUOSeconds)
	}
	if m.CostPerTurn < 0.59 || m.CostPerTurn > 0.61 {
		t.Errorf("cost/turn = %.4f; want ~0.60", m.CostPerTurn)
	}
}

// Open tasks (no CompletedAt) use clock() as the upper bound.
func TestMetricsForOpenTask(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	start := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	if err := store.Append(ctx, &eventschema.Envelope{
		ID: "e1", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: start.Add(time.Minute), Source: "test",
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-7",
			InputTokens: 100, OutputTokens: 10, CostUSD: 0.1,
		},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	clock := func() time.Time { return start.Add(5 * time.Minute) }
	m, err := MetricsFor(ctx, store, Task{StartedAt: start}, clock)
	if err != nil {
		t.Fatalf("MetricsFor: %v", err)
	}
	if m.Turns != 1 {
		t.Errorf("turns = %d; want 1", m.Turns)
	}
	if m.Duration != 5*time.Minute {
		t.Errorf("duration = %v; want 5m", m.Duration)
	}
}

// Nil store returns zero metrics (Duration still computes).
func TestMetricsForNilStore(t *testing.T) {
	m, err := MetricsFor(context.Background(), nil, Task{
		StartedAt:   time.Now().Add(-time.Minute),
		CompletedAt: time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("nil store should not error: %v", err)
	}
	if m.Turns != 0 || m.CostUSD != 0 {
		t.Errorf("expected zero metrics: %+v", m)
	}
}
