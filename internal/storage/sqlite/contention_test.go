package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Another process holding the write lock is the common way an append
// fails on a shared store. The store must say so, because the bus retries
// contention until it clears and drops anything else after a bounded budget.
func TestAppendBatchReportsLockContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	ctx := context.Background()
	s, err := Open(ctx, path, Options{BusyTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	holder, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	conn, err := holder.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.ExecContext(ctx, "ROLLBACK") })

	env := mustPromptEnvelope(t, "contended-1", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
	err = s.AppendBatch(ctx, []*eventschema.Envelope{env})
	if err == nil {
		t.Fatal("AppendBatch succeeded while another connection held the write lock")
	}
	if !IsContended(err) {
		t.Fatalf("IsContended(%v) = false, want true", err)
	}
}

// Running out of the caller's time budget is contention from the store's
// point of view: the same batch succeeds once the other writer finishes.
func TestAppendBatchDeadlineIsContention(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	env := mustPromptEnvelope(t, "late-1", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
	err := s.AppendBatch(ctx, []*eventschema.Envelope{env})
	if err == nil {
		t.Fatal("AppendBatch succeeded with an expired context")
	}
	if !IsContended(err) {
		t.Fatalf("IsContended(%v) = false, want true", err)
	}
}

// A malformed envelope will fail identically on every attempt, so it must
// not be reported as contention or the bus would retry it forever. In a
// batch it is skipped outright; alone, Append reports it.
func TestAppendBatchMalformedIsNotContention(t *testing.T) {
	s := newTestStore(t)
	if err := s.AppendBatch(context.Background(), []*eventschema.Envelope{{ID: "broken"}}); err != nil {
		t.Fatalf("AppendBatch = %v, want the malformed envelope skipped", err)
	}
	if s.SkippedInvalid() != 1 {
		t.Fatalf("SkippedInvalid = %d, want 1", s.SkippedInvalid())
	}
	err := s.Append(context.Background(), &eventschema.Envelope{ID: "broken"})
	if err == nil {
		t.Fatal("Append accepted an envelope with no payload")
	}
	if IsContended(err) {
		t.Fatalf("IsContended(%v) = true, want false", err)
	}
	if IsContended(nil) || IsContended(errors.New("disk full")) {
		t.Fatal("IsContended must be false for nil and unrelated errors")
	}
}

// The store holds usage history for every agent session on the machine.
// It gets the same owner-only mode as the other files under ~/.tokenops,
// including a store created by an older release with the umask default.
func TestOpenRestrictsStoreToOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	env := mustPromptEnvelope(t, "perm-1", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
	if err := s.Append(context.Background(), env); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = s.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			if err := os.Chmod(p, 0o644); err != nil {
				t.Fatalf("widen %s: %v", p, err)
			}
		}
	}

	s, err = Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), mode)
		}
	}
}
