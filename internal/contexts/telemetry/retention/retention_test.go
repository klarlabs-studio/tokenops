package retention

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// A pass that deletes nothing must still checkpoint the WAL. Left alone,
// the -wal file grows without bound while readers keep the automatic
// checkpoint from ever resetting it; on one real machine it reached 372MB
// beside a 396MB database, and every insert then took long enough that
// whole batches timed out and were discarded.
func TestPruneCheckpointsWALEvenWithNoDeletions(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	// Write enough to push pages into the WAL.
	for i := 0; i < 400; i++ {
		env := mkPrompt("wal-"+string(rune('a'+i%26))+string(rune('a'+i/26)), time.Now().UTC())
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	p := New(store, Config{
		Logger:  discardLogger(),
		Reclaim: true,
		Policies: []Policy{
			// A window wide enough that nothing is eligible for deletion.
			{EventType: eventschema.EventTypePrompt, KeepFor: 100 * 365 * 24 * time.Hour},
		},
	})
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, r := range res {
		if r.Deleted != 0 {
			t.Fatalf("expected no deletions, got %d", r.Deleted)
		}
	}

	var busy, logSize, checkpointed int
	row := store.DB().QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	if err := row.Scan(&busy, &logSize, &checkpointed); err != nil {
		t.Fatalf("wal_checkpoint: %v", err)
	}
	// After a TRUNCATE checkpoint in the pass, the WAL should hold
	// (almost) nothing. A large residual means the pass never ran one.
	if logSize > 64 {
		t.Errorf("WAL still holds %d frames after prune; checkpoint did not run", logSize)
	}
}

func mkPromptFrom(id, source string, ts time.Time) *eventschema.Envelope {
	e := mkPrompt(id, ts)
	e.Source = source
	return e
}

// Every vendor-usage reader writes type "prompt", so a per-type window is
// the only knob an operator has and it governs Claude Code, Codex,
// opencode and Cursor alike. A source window lets imported history be
// kept on its own terms.
func TestSourcePolicyOverridesTypePolicy(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	old := time.Now().Add(-200 * 24 * time.Hour).UTC()

	for _, e := range []*eventschema.Envelope{
		mkPromptFrom("keep-1", "opencode", old),
		mkPromptFrom("keep-2", "opencode", old),
		mkPromptFrom("drop-1", "claude-code-jsonl", old),
	} {
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	p := New(store, Config{
		Logger: discardLogger(),
		Policies: []Policy{
			{EventType: eventschema.EventTypePrompt, KeepFor: 120 * 24 * time.Hour},
			// Keep opencode forever: it is imported history, not live noise.
			{EventType: eventschema.EventTypePrompt, Source: "opencode", KeepFor: 0},
		},
	})
	if _, err := p.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	var opencodeLeft, claudeLeft int
	row := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE source = 'opencode'`)
	if err := row.Scan(&opencodeLeft); err != nil {
		t.Fatalf("count opencode: %v", err)
	}
	row = store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE source = 'claude-code-jsonl'`)
	if err := row.Scan(&claudeLeft); err != nil {
		t.Fatalf("count claude: %v", err)
	}

	if opencodeLeft != 2 {
		t.Errorf("opencode rows = %d, want 2: the source policy must win over the type policy", opencodeLeft)
	}
	if claudeLeft != 0 {
		t.Errorf("claude-code-jsonl rows = %d, want 0: the type policy still applies to it", claudeLeft)
	}
}

// A shorter source window must prune even when the type window would keep
// the row, so the override works in both directions.
func TestSourcePolicyCanBeShorterThanTypePolicy(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	ts := time.Now().Add(-30 * 24 * time.Hour).UTC()

	for _, e := range []*eventschema.Envelope{
		mkPromptFrom("noisy-1", "read-guard", ts),
		mkPromptFrom("kept-1", "claude-code-jsonl", ts),
	} {
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	p := New(store, Config{
		Logger: discardLogger(),
		Policies: []Policy{
			{EventType: eventschema.EventTypePrompt, KeepFor: 365 * 24 * time.Hour},
			{EventType: eventschema.EventTypePrompt, Source: "read-guard", KeepFor: 7 * 24 * time.Hour},
		},
	})
	if _, err := p.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	var guard, claude int
	_ = store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE source='read-guard'`).Scan(&guard)
	_ = store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE source='claude-code-jsonl'`).Scan(&claude)
	if guard != 0 {
		t.Errorf("read-guard rows = %d, want 0", guard)
	}
	if claude != 1 {
		t.Errorf("claude-code-jsonl rows = %d, want 1", claude)
	}
}

// A daemon starting up replays its sources' history into the store, and a
// prune with Reclaim holds the write lock for as long as the VACUUM takes —
// 31s on a real 440 MB store. Run together, the replay's batches failed four
// attempts in a row and cleared on their last. StartDelay moves the first
// pass out of that window.
func TestSchedulerHoldsTheFirstPassForStartDelay(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_ = store.Append(ctx, mkPrompt("old", now.Add(-48*time.Hour)))

	p := New(store, Config{
		Policies:   []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: 24 * time.Hour}},
		Interval:   time.Hour,
		StartDelay: 300 * time.Millisecond,
		Logger:     discardLogger(),
	})
	p.SetClock(func() time.Time { return now })
	s := NewScheduler(p)
	runCtx, cancel := context.WithCancel(ctx)
	defer func() { cancel(); s.Wait() }()
	s.Start(runCtx)

	time.Sleep(100 * time.Millisecond)
	if count, _ := store.Count(ctx, sqlite.Filter{}); count != 1 {
		t.Fatalf("pruned before StartDelay elapsed: count=%d", count)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count, _ := store.Count(ctx, sqlite.Filter{}); count == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("did not prune once StartDelay had elapsed")
}

// Shutting down during the delay must not wait it out.
func TestSchedulerStopsDuringStartDelay(t *testing.T) {
	p := New(newStore(t), Config{
		Policies:   []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: 24 * time.Hour}},
		StartDelay: time.Hour,
		Logger:     discardLogger(),
	})
	s := NewScheduler(p)
	runCtx, cancel := context.WithCancel(context.Background())
	s.Start(runCtx)
	cancel()

	stopped := make(chan struct{})
	go func() { s.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("scheduler kept waiting out StartDelay after its context was cancelled")
	}
}
