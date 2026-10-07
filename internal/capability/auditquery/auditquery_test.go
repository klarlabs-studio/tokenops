package auditquery

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

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
			if err == nil && q.filter.Limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", q.filter.Limit, tt.wantLimit)
			}
		})
	}
}

func TestParseDefaultsToTheLastDay(t *testing.T) {
	q, err := Parse(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(q.filter.Since); age < 23*time.Hour || age > 25*time.Hour {
		t.Errorf("since is %v ago, want about 24h", age)
	}
}

func TestListFiltersByAction(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rec := audit.NewRecorder(store)
	for _, a := range []audit.Action{audit.ActionBudgetExceeded, audit.ActionOptimizationApply} {
		if _, err := rec.Record(ctx, audit.Entry{Action: a, Actor: "t", Timestamp: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	q, err := Parse(Request{Action: string(audit.ActionOptimizationApply)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewLog(store).List(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Action != audit.ActionOptimizationApply {
		t.Errorf("entries = %+v", got.Entries)
	}
	if NewLog(nil) != nil {
		t.Error("NewLog(nil) should be nil")
	}
}
