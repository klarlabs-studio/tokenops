package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// seedAuditDB writes audit entries at fixed instants, one detail each so
// the text rendering (which ranges a map) is deterministic.
func seedAuditDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rec := audit.NewRecorder(store)
	base := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	for i, e := range []audit.Entry{
		{ID: "a1", Action: audit.ActionConfigChange, Actor: "cli", Target: "plans.anthropic", Details: map[string]any{"to": "claude-max-5x"}},
		{ID: "a2", Action: audit.ActionBudgetUpdate, Actor: "mcp", Target: "budget.daily"},
		{ID: "a3", Action: audit.ActionConfigChange, Actor: "mcp", Target: "mode", Details: map[string]any{"to": "advise"}},
	} {
		e.Timestamp = base.Add(time.Duration(i) * time.Hour)
		if _, err := rec.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestAuditOutputCharacterization pins `tokenops audit` byte for byte.
func TestAuditOutputCharacterization(t *testing.T) {
	path := seedAuditDB(t)
	cases := []struct {
		golden string
		args   []string
	}{
		{"audit/all.txt", nil},
		{"audit/all.json", []string{"--json"}},
		{"audit/filtered.txt", []string{"--actor", "mcp", "--since", "2026-01-02T09:30:00Z", "--until", "2026-01-02T12:00:00Z", "--limit", "1"}},
		{"audit/none.txt", []string{"--action", "no-such-action"}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out, err := executeRoot(t, append([]string{"audit", "--db", path}, tc.args...)...)
			if err != nil {
				t.Fatalf("audit: %v", err)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}
