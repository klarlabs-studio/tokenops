package retention

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mkPrompt(id string, ts time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: ts, Source: "ret-test",
		Payload: &eventschema.PromptEvent{
			PromptHash: "h", Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
		},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPruneDeletesOlderThanWindow(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("old", now.Add(-48*time.Hour)),
		mkPrompt("recent", now.Add(-time.Hour)),
		mkPrompt("fresh", now),
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	p := New(store, Config{
		Policies: []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: 24 * time.Hour}},
		Logger:   discardLogger(),
	})
	p.SetClock(func() time.Time { return now })

	res, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res) != 1 || res[0].Deleted != 1 {
		t.Errorf("result: %+v", res)
	}
	count, _ := store.Count(ctx, sqlite.Filter{})
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestPruneZeroWindowSkipped(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	_ = store.Append(ctx, mkPrompt("x", time.Now().UTC()))
	p := New(store, Config{
		Policies: []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: 0}},
		Logger:   discardLogger(),
	})
	res, _ := p.Run(ctx)
	if len(res) != 0 {
		t.Errorf("zero window should skip: %+v", res)
	}
}

func TestPruneByEventType(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	prompt := mkPrompt("p", now.Add(-100*time.Hour))
	wf := &eventschema.Envelope{
		ID: "w", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeWorkflow, Timestamp: now.Add(-100 * time.Hour),
		Payload: &eventschema.WorkflowEvent{WorkflowID: "wf", State: eventschema.WorkflowStateProgress},
	}
	_ = store.AppendBatch(ctx, []*eventschema.Envelope{prompt, wf})

	p := New(store, Config{
		Policies: []Policy{
			{EventType: eventschema.EventTypePrompt, KeepFor: 24 * time.Hour},
			// workflow has no policy → not pruned
		},
		Logger: discardLogger(),
	})
	p.SetClock(func() time.Time { return now })

	_, _ = p.Run(ctx)
	pCount, _ := store.Count(ctx, sqlite.Filter{Type: eventschema.EventTypePrompt})
	wCount, _ := store.Count(ctx, sqlite.Filter{Type: eventschema.EventTypeWorkflow})
	if pCount != 0 || wCount != 1 {
		t.Errorf("counts: prompt=%d workflow=%d", pCount, wCount)
	}
}

func TestNilPrunerReturnsError(t *testing.T) {
	var p *Pruner
	if _, err := p.Run(context.Background()); err == nil {
		t.Error("expected error")
	}
}

func TestSchedulerRunsImmediately(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_ = store.Append(ctx, mkPrompt("old", now.Add(-48*time.Hour)))

	var calls atomic.Int64
	clock := func() time.Time {
		calls.Add(1)
		return now
	}
	p := New(store, Config{
		Policies: []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: 24 * time.Hour}},
		Interval: time.Hour,
		Logger:   discardLogger(),
	})
	p.SetClock(clock)
	s := NewScheduler(p)
	runCtx, cancel := context.WithCancel(ctx)
	s.Start(runCtx)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		count, _ := store.Count(ctx, sqlite.Filter{})
		if count == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	count, _ := store.Count(ctx, sqlite.Filter{})
	if count != 0 {
		t.Errorf("scheduler did not prune: count=%d", count)
	}
	cancel()
	s.Wait()
}

func TestDefaultIntervalApplied(t *testing.T) {
	p := New(nil, Config{})
	if p.cfg.Interval != time.Hour {
		t.Errorf("default interval = %s", p.cfg.Interval)
	}
}
