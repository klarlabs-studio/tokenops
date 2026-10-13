package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// appendRows writes n prompt events in batches of 64, the event bus's size,
// each about a page long so the WAL passes SQLite's threshold quickly.
func appendRows(t *testing.T, s *Store, prefix string, n int) {
	t.Helper()
	ctx := context.Background()
	base := time.Now()
	filler := map[string]string{"filler": strings.Repeat("x", 4000)}
	for i := 0; i < n; i += 64 {
		batch := make([]*eventschema.Envelope, 0, 64)
		for j := i; j < i+64 && j < n; j++ {
			env := mustPromptEnvelope(t, fmt.Sprintf("%s-%d", prefix, j), base.Add(time.Duration(j)*time.Second),
				&eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-5-5", InputTokens: int64(j)})
			env.Attributes = filler
			batch = append(batch, env)
		}
		if err := s.AppendBatch(ctx, batch); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
}

// walFrames reports how many frames the WAL holds. A frame count above
// SQLite's 1000-page threshold means no commit has checkpointed and reset
// it. (The pragma backfills as a side effect; callers read it last.)
func walFrames(t *testing.T, s *Store) int64 {
	t.Helper()
	var busy, log, done int64
	if err := s.DB().QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &done); err != nil {
		t.Fatalf("wal_checkpoint: %v", err)
	}
	return log
}

// The daemon is the store's main writer. A commit that crosses SQLite's
// auto-checkpoint threshold runs the checkpoint, and its sync, while it
// still holds the write lock; with long readers open the WAL cannot be
// reset, so every commit retries it. Measured on a 451 MB store with
// three month-long reads running, 64-row appends held the lock for up to
// 1.3s; with the checkpoint taken off the commit, under 0.2s. With
// BackgroundCheckpoint set, commits never checkpoint.
func TestBackgroundCheckpointKeepsCheckpointsOutOfCommits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(context.Background(), path, Options{BackgroundCheckpoint: time.Hour})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var auto int
	if err := s.DB().QueryRow("PRAGMA wal_autocheckpoint").Scan(&auto); err != nil {
		t.Fatalf("read wal_autocheckpoint: %v", err)
	}
	if auto != 0 {
		t.Fatalf("wal_autocheckpoint = %d, want 0: commits would checkpoint under the write lock", auto)
	}
	appendRows(t, s, "bg", 1500)
	if got := walFrames(t, s); got < 1400 {
		t.Fatalf("WAL holds %d frames after 1500 rows; a commit checkpointed it", got)
	}
}

// Every other process keeps SQLite's default, so a store written without
// a daemon running never grows its WAL without bound.
func TestDefaultOpenKeepsAutoCheckpoint(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var auto int
	if err := s.DB().QueryRow("PRAGMA wal_autocheckpoint").Scan(&auto); err != nil {
		t.Fatalf("read wal_autocheckpoint: %v", err)
	}
	if auto != 1000 {
		t.Fatalf("wal_autocheckpoint = %d, want SQLite's default 1000", auto)
	}
}

// The background loop backfills the WAL on its own connection, so the
// WAL does not grow while the daemon runs, and Close stops it.
func TestBackgroundCheckpointBackfillsTheWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(context.Background(), path, Options{BackgroundCheckpoint: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	appendRows(t, s, "loop", 1500)
	deadline := time.Now().Add(5 * time.Second)
	for {
		last := s.lastCheckpoint.Load()
		if last != nil && last.Frames > 0 && last.Backfilled == last.Frames {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background checkpoint never backfilled the WAL: %d runs, last %+v", s.checkpoints.Load(), last)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	after := s.checkpoints.Load()
	time.Sleep(100 * time.Millisecond)
	if s.checkpoints.Load() != after {
		t.Fatal("background checkpoint kept running after Close")
	}
}
