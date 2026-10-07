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

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		req       Request
		wantErr   bool
		wantLimit int
	}{
		{"defaults", Request{}, false, defaultLimit},
		{"explicit limit", Request{Limit: "5"}, false, 5},
		{"bad limit falls back", Request{Limit: "lots"}, false, defaultLimit},
		{"negative limit falls back", Request{Limit: "-3"}, false, defaultLimit},
		{"bad since", Request{Since: "nope"}, true, 0},
		{"bad until", Request{Until: "nope"}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := Parse(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && q.Limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", q.Limit, tt.wantLimit)
			}
		})
	}
}

func TestParseDefaultsToTheLastDay(t *testing.T) {
	q, err := Parse(Request{Action: "config_change", Actor: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(q.Since); age < 23*time.Hour || age > 25*time.Hour {
		t.Errorf("since is %v ago, want about 24h", age)
	}
	if q.Action != "config_change" || q.Actor != "api" || !q.Until.IsZero() {
		t.Errorf("query %+v", q)
	}
}
