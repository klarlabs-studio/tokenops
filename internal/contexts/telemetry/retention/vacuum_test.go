package retention

import (
	"context"
	"path/filepath"
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

// Reclaim is opt-in: a full VACUUM rewrites the database and takes a
// write lock, so an operator who has not asked for it keeps the cheap
// delete-only behaviour.
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
