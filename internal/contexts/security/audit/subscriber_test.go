package audit

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func waitForAuditEntries(t *testing.T, rec *Recorder, want int) []Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := rec.Query(context.Background(), Filter{Limit: 50})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(entries) >= want {
			return entries
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not observe %d audit entries within deadline", want)
	return nil
}

func newAuditEventBus() *events.AsyncBus {
	return events.NewAsync(events.NoopSink{}, events.Options{})
}

func publishAuditEvent(t *testing.T, bus *events.AsyncBus, kind string, at time.Time, data any) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	bus.Publish(&eventschema.Envelope{
		ID: "test-event", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeDomain, Timestamp: at,
		Payload: &eventschema.DomainEvent{Kind: kind, Data: raw},
	})
}

func TestSubscribeRecordsBudgetExceeded(t *testing.T) {
	store := openStore(t)
	rec := NewRecorder(store)
	bus := newAuditEventBus()
	sub := Subscribe(bus, rec, nil, "tester")

	publishAuditEvent(t, bus, "budget.exceeded", time.Now().UTC(), map[string]any{
		"BudgetID": "weekly", "SpentUSD": 150, "LimitUSD": 100,
	})
	entries := waitForAuditEntries(t, rec, 1)
	sub.Close()
	_ = bus.Close(time.Second)
	if entries[0].Action != ActionBudgetExceeded {
		t.Errorf("Action = %q, want %q", entries[0].Action, ActionBudgetExceeded)
	}
	if entries[0].Target != "weekly" {
		t.Errorf("Target = %q, want weekly", entries[0].Target)
	}
}

func TestSubscribeRecordsOptimizationApplied(t *testing.T) {
	store := openStore(t)
	rec := NewRecorder(store)
	bus := newAuditEventBus()
	sub := Subscribe(bus, rec, nil, "tester")

	publishAuditEvent(t, bus, "optimization.applied", time.Now().UTC(), map[string]any{
		"PromptHash": "sha256:abc", "OptimizerKind": "prompt_compress", "TokensSaved": 500,
	})
	entries := waitForAuditEntries(t, rec, 1)
	sub.Close()
	_ = bus.Close(time.Second)
	if entries[0].Action != ActionOptimizationApply {
		t.Errorf("Action = %q", entries[0].Action)
	}
	if entries[0].Target != "prompt_compress" {
		t.Errorf("Target = %q", entries[0].Target)
	}
}

func TestSubscribeIgnoresUnknownEvent(t *testing.T) {
	store := openStore(t)
	rec := NewRecorder(store)
	bus := newAuditEventBus()
	sub := Subscribe(bus, rec, nil, "tester")

	publishAuditEvent(t, bus, "workflow.started", time.Now().UTC(), map[string]any{"WorkflowID": "wf-1"})
	// Should not record — recorder.Query returns 0 even after a brief
	// goroutine drain wait.
	time.Sleep(100 * time.Millisecond)
	entries, _ := rec.Query(context.Background(), Filter{Limit: 10})
	sub.Close()
	_ = bus.Close(time.Second)
	if len(entries) != 0 {
		t.Errorf("expected no entries, got %d", len(entries))
	}
}

func TestSubscribeBackpressureDropsExcess(t *testing.T) {
	store := openStore(t)
	rec := NewRecorder(store)
	bus := newAuditEventBus()
	sub := SubscribeWithOptions(bus, rec, nil, SubscribeOptions{Actor: "tester", MaxConcurrent: 1})
	if sub == nil {
		t.Fatal("subscriber nil")
	}
	// Burst far above MaxConcurrent. With a single worker slot most
	// events should be shed instead of spawning goroutines unbounded.
	for range 200 {
		publishAuditEvent(t, bus, "optimization.applied", time.Now().UTC(), map[string]any{
			"OptimizerKind": "prompt_compress", "TokensSaved": 50,
		})
	}
	// Some drops expected.
	if sub.DroppedCount() == 0 {
		t.Errorf("expected DroppedCount > 0 under backpressure")
	}
	sub.Close()
	_ = bus.Close(time.Second)
}
