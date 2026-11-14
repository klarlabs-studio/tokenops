package workflowtrace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// cacheNow is the real time: the change log logs changes to events by how
// old they are now.
var cacheNow = time.Now().UTC().Truncate(time.Second)

func turnOf(id, workflowID string, at time.Time, in int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: at, Source: "test",
		Payload: &eventschema.PromptEvent{
			PromptHash: id, Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o-mini",
			InputTokens: in, OutputTokens: in / 10, TotalTokens: in + in/10, Status: 200, WorkflowID: workflowID,
		},
	}
}

// rawDB opens the store's file directly, for the changes the store's API
// does not make.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// The cache answers what reconstructing from the store answers, through
// every kind of change, and reconstructs a workflow again only when a
// change touched it or the rate card changed.
func TestTraceCacheMatchesReconstruct(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/e.db"
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := rawDB(t, path)
	envs := make([]*eventschema.Envelope, 0, 120)
	for i := range 120 {
		envs = append(envs, turnOf(fmt.Sprintf("e%03d", i), fmt.Sprintf("wf%d", i%4), cacheNow.Add(-time.Duration(120-i)*time.Minute), int64(100+i)))
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	eng := spend.NewEngine(spend.DefaultTable())
	c := NewTraceCache(store, eng, time.Hour)
	ids := []string{"wf0", "wf1", "wf2", "wf3", "none"}
	exec := func(stmts ...string) {
		t.Helper()
		for _, q := range stmts {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	check := func(step string, rebuilt int) {
		t.Helper()
		before := c.rebuilt
		for _, id := range ids {
			want, werr := workflow.Reconstruct(ctx, store, eng, id)
			got, gerr := c.Reconstruct(ctx, id)
			if !errors.Is(gerr, werr) && (werr != nil || gerr != nil) {
				t.Fatalf("%s, %s: error %v, want %v", step, id, gerr, werr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s, %s: trace differs:\ngot  %+v\nwant %+v", step, id, got, want)
			}
		}
		if n := c.rebuilt - before; rebuilt >= 0 && n != rebuilt {
			t.Fatalf("%s: %d traces reconstructed, want %d", step, n, rebuilt)
		}
	}
	check("first read", len(ids))
	steps := []struct {
		name    string
		do      func()
		rebuilt int
	}{
		{"nothing changed", func() {}, 0},
		{"a turn of another workflow", func() {
			if err := store.Append(ctx, turnOf("x1", "other", cacheNow, 5)); err != nil {
				t.Fatal(err)
			}
		}, 0},
		{"a turn of wf0", func() {
			if err := store.Append(ctx, turnOf("n1", "wf0", cacheNow, 7)); err != nil {
				t.Fatal(err)
			}
		}, 1},
		{"an update of a wf1 turn", func() {
			exec(`UPDATE events SET payload = json_set(payload, '$.input_tokens', 9999), input_tokens = 9999 WHERE id = 'e001'`)
		}, 1},
		{"an update of another workflow's turn", func() {
			exec(`UPDATE events SET input_tokens = 3 WHERE id = 'x1'`)
		}, 0},
		{"a turn moved from wf2 to wf3", func() {
			exec(`UPDATE events SET workflow_id = 'wf3', payload = json_set(payload, '$.workflow_id', 'wf3') WHERE id = 'e002'`)
		}, 2},
		{"a turn joins a workflow that had none", func() {
			exec(`UPDATE events SET workflow_id = 'none', payload = json_set(payload, '$.workflow_id', 'none') WHERE id = 'x1'`)
		}, 1},
		{"a delete", func() { exec(`DELETE FROM events WHERE id = 'e004'`) }, 1},
		{"the newest row deleted and its rowid reused", func() {
			exec(`DELETE FROM events WHERE rowid = (SELECT MAX(rowid) FROM events)`) // n1, of wf0
			if err := store.Append(ctx, turnOf("r1", "wf1", cacheNow, 11)); err != nil {
				t.Fatal(err)
			}
		}, 2},
		{"a new rate card", func() { eng.Replace([]spend.DatedTable{{Table: spend.DefaultTable()}}) }, len(ids)},
		// Whether a VACUUM renumbers rows is SQLite's choice: the answers
		// must hold either way, however many it reconstructs.
		{"a vacuum", func() { exec(`DELETE FROM events WHERE id IN ('e010', 'e020')`, `VACUUM`) }, -1},
		{"after the vacuum", func() {}, 0},
	}
	for _, st := range steps {
		st.do()
		check(st.name, st.rebuilt)
	}
}

// A trace nobody read for the idle time is forgotten.
func TestTraceCacheForgetsIdleTraces(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	if err := store.Append(ctx, turnOf("a", "wf", cacheNow, 10)); err != nil {
		t.Fatal(err)
	}
	c := NewTraceCache(store, nil, time.Hour)
	at := cacheNow
	c.now = func() time.Time { return at }
	if _, err := c.Reconstruct(ctx, "wf"); err != nil {
		t.Fatal(err)
	}
	at = at.Add(2 * time.Hour)
	if _, err := c.Reconstruct(ctx, "other"); !errors.Is(err, workflow.ErrNoTrace) {
		t.Fatalf("an unknown workflow gave %v", err)
	}
	if _, held := c.entries["wf"]; held || len(c.entries) != 1 {
		t.Fatalf("an idle trace was kept: %d held", len(c.entries))
	}
}

// Reconstructing a period's workflows, from the store as before and
// through a warm cache.
func BenchmarkTraceCache(b *testing.B) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, b.TempDir()+"/e.db", sqlite.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	const workflows, steps = 40, 250
	envs := make([]*eventschema.Envelope, 0, workflows*steps)
	for i := range workflows * steps {
		envs = append(envs, turnOf(fmt.Sprintf("e%05d", i), fmt.Sprintf("wf%02d", i%workflows),
			cacheNow.Add(-time.Duration(workflows*steps-i)*time.Second), int64(100+i%500)))
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		b.Fatal(err)
	}
	eng := spend.NewEngine(spend.DefaultTable())
	read := func(b *testing.B, reconstruct func(context.Context, string) (*workflow.Trace, error)) {
		for i := range workflows {
			if _, err := reconstruct(ctx, fmt.Sprintf("wf%02d", i)); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("store", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			read(b, func(ctx context.Context, id string) (*workflow.Trace, error) {
				return workflow.Reconstruct(ctx, store, eng, id)
			})
		}
	})
	b.Run("cache-warm", func(b *testing.B) {
		c := NewTraceCache(store, eng, time.Hour)
		read(b, c.Reconstruct)
		b.ReportAllocs()
		for b.Loop() {
			read(b, c.Reconstruct)
		}
	})
}
