package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
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

func TestSubscribeRecordsBudgetExceeded(t *testing.T) {
	store := openStore(t)
	rec := NewRecorder(store)
	bus := &domainevents.Bus{}
	Subscribe(bus, rec, nil, "tester")

	bus.Publish(domainevents.BudgetExceeded{
		BudgetID: "weekly",
		SpentUSD: 150,
		LimitUSD: 100,
		At:       time.Now().UTC(),
	})
	entries := waitForAuditEntries(t, rec, 1)
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
	bus := &domainevents.Bus{}
	Subscribe(bus, rec, nil, "tester")

	bus.Publish(domainevents.OptimizationApplied{
		PromptHash:    "sha256:abc",
		OptimizerKind: "prompt_compress",
		TokensSaved:   500,
		At:            time.Now().UTC(),
	})
	entries := waitForAuditEntries(t, rec, 1)
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
	bus := &domainevents.Bus{}
	Subscribe(bus, rec, nil, "tester")

	bus.Publish(domainevents.WorkflowStarted{WorkflowID: "wf-1", At: time.Now()})
	// Should not record — recorder.Query returns 0 even after a brief
	// goroutine drain wait.
	time.Sleep(100 * time.Millisecond)
	entries, _ := rec.Query(context.Background(), Filter{Limit: 10})
	if len(entries) != 0 {
		t.Errorf("expected no entries, got %d", len(entries))
	}
}

func TestSubscribeBackpressureDropsExcess(t *testing.T) {
	store := openStore(t)
	rec := NewRecorder(store)
	bus := &domainevents.Bus{}
	sub := SubscribeWithOptions(bus, rec, nil, SubscribeOptions{Actor: "tester", MaxConcurrent: 1})
	if sub == nil {
		t.Fatal("subscriber nil")
	}
	// Burst far above MaxConcurrent. With a single worker slot most
	// events should be shed instead of spawning goroutines unbounded.
	for range 200 {
		bus.Publish(domainevents.OptimizationApplied{OptimizerKind: "prompt_compress", At: time.Now()})
	}
	// Some drops expected.
	if sub.DroppedCount() == 0 {
		t.Errorf("expected DroppedCount > 0 under backpressure")
	}
}
