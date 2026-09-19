package retention

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seed(t *testing.T, st *sqlite.Store, n int, at time.Time) {
	t.Helper()
	envs := make([]*eventschema.Envelope, 0, n)
	for i := range n {
		envs = append(envs, &eventschema.Envelope{
			ID:            filepath.Join("e", time.Duration(i).String()),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     at,
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "m",
				InputTokens: 100, OutputTokens: 10, TotalTokens: 110,
			},
		})
	}
	if err := st.AppendBatch(context.Background(), envs); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// Deleting rows leaves the pages on the freelist: SQLite never shrinks
// the file on its own with auto_vacuum off. Without a reclaim step the
// store keeps its high-water mark forever, so pruning frees nothing an
// operator can actually see on disk.
func TestPruneReclaimsFreelistPages(t *testing.T) {
	st := openStore(t)
	old := time.Now().Add(-72 * time.Hour)
	seed(t, st, 400, old)

	p := New(st, Config{
		Policies: []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: time.Hour}},
		Reclaim:  true,
	})
	if _, err := p.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	var freelist int
	if err := st.DB().QueryRowContext(context.Background(), "PRAGMA freelist_count").Scan(&freelist); err != nil {
		t.Fatalf("freelist: %v", err)
	}
	if freelist != 0 {
		t.Errorf("freelist_count = %d, want 0 after a reclaiming prune", freelist)
	}
}

// Reclaim is opt-in, so an operator who has not asked for it keeps the
// cheap delete-only behaviour. Incremental auto-vacuum frees nothing on its
// own; pages stay on the freelist until a reclaim asks for them.
func TestReclaimDisabledLeavesFreePages(t *testing.T) {
	st := openStore(t)
	seed(t, st, 400, time.Now().Add(-72*time.Hour))

	p := New(st, Config{
		Policies: []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: time.Hour}},
	})
	if _, err := p.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var freelist int
	if err := st.DB().QueryRowContext(context.Background(), "PRAGMA freelist_count").Scan(&freelist); err != nil {
		t.Fatalf("freelist: %v", err)
	}
	if freelist == 0 {
		t.Skip("engine reclaimed pages on its own; nothing to assert")
	}
}

// A pass that deletes nothing must not pay for a VACUUM.
func TestNoDeletionsSkipsReclaim(t *testing.T) {
	st := openStore(t)
	seed(t, st, 10, time.Now())
	p := New(st, Config{
		Policies: []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: 24 * time.Hour}},
		Reclaim:  true,
	})
	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, r := range res {
		if r.Deleted != 0 {
			t.Fatalf("expected no deletions, got %d", r.Deleted)
		}
	}
}

// legacyStore opens a store whose file predates incremental auto-vacuum:
// the database already exists, with auto_vacuum off, when the store first
// opens it.
func legacyStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=auto_vacuum(NONE)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("CREATE TABLE predates_incremental(x)"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	st, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if autoVacuumMode(t, st) != 0 {
		t.Fatal("fixture is not a legacy store: auto_vacuum already on")
	}
	return st
}

func autoVacuumMode(t *testing.T, st *sqlite.Store) int {
	t.Helper()
	var mode int
	if err := st.DB().QueryRowContext(context.Background(), "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

func freelistCount(t *testing.T, st *sqlite.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(), "PRAGMA freelist_count").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A full VACUUM held the write lock for 31s on a 440 MB store to return
// ~100 pages, and every write landing in that window failed until it ended.
// A legacy store pays for one more full VACUUM, which converts it; every
// reclaim after that frees pages in short chunks.
func TestReclaimConvertsALegacyStoreOnceThenRunsIncrementally(t *testing.T) {
	st := legacyStore(t)
	var logs bytes.Buffer
	p := New(st, Config{
		Policies: []Policy{{EventType: eventschema.EventTypePrompt, KeepFor: time.Hour}},
		Reclaim:  true,
		Logger:   slog.New(slog.NewTextHandler(&logs, nil)),
	})

	seed(t, st, 400, time.Now().Add(-72*time.Hour))
	if _, err := p.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if autoVacuumMode(t, st) != 2 {
		t.Fatalf("auto_vacuum = %d after the first reclaim, want 2 (incremental)", autoVacuumMode(t, st))
	}
	if !strings.Contains(logs.String(), "mode=convert") {
		t.Errorf("first reclaim on a legacy store did not report the conversion:\n%s", logs.String())
	}

	logs.Reset()
	seed(t, st, 400, time.Now().Add(-72*time.Hour))
	if _, err := p.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(logs.String(), "mode=incremental") {
		t.Errorf("reclaim on a converted store did not run incrementally:\n%s", logs.String())
	}
	if n := freelistCount(t, st); n != 0 {
		t.Errorf("freelist_count = %d after an incremental reclaim, want 0", n)
	}
}
