package auditlog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func TestReadFiltersNewestFirst(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rec := audit.NewRecorder(store)
	base := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	for i, e := range []audit.Entry{
		{ID: "old", Action: audit.ActionConfigChange, Actor: "cli"},
		{ID: "other", Action: audit.ActionBudgetUpdate, Actor: "mcp"},
		{ID: "new", Action: audit.ActionConfigChange, Actor: "mcp"},
	} {
		e.Timestamp = base.Add(time.Duration(i) * time.Hour)
		if _, err := rec.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	all, err := Read(ctx, store, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Entries) != 3 || all.Entries[0].ID != "new" {
		t.Fatalf("all = %+v", all.Entries)
	}
	got, err := Read(ctx, store, Query{Action: "config_change", Actor: "mcp", Since: base.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].ID != "new" {
		t.Fatalf("filtered = %+v", got.Entries)
	}
	limited, err := Read(ctx, store, Query{Limit: 2, Until: base.Add(90 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Entries) != 2 || limited.Entries[0].ID != "other" {
		t.Fatalf("limited = %+v", limited.Entries)
	}
}
