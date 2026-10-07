package mcp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// TestAuditToolCharacterization pins tokenops_audit's result so moving its
// query into internal/capability cannot change what an agent reads.
func TestAuditToolCharacterization(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "audit.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rec := audit.NewRecorder(store)
	base := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	for i, e := range []audit.Entry{
		{ID: "a1", Action: audit.ActionConfigChange, Actor: "cli", Target: "plans.anthropic", Details: map[string]any{"to": "claude-max-5x"}},
		{ID: "a2", Action: audit.ActionBudgetUpdate, Actor: "mcp", Target: "budget.daily"},
		{ID: "a3", Action: audit.ActionConfigChange, Actor: "mcp", Target: "mode", Details: map[string]any{"to": "advise", "from": "observe"}},
	} {
		e.Timestamp = base.Add(time.Duration(i) * time.Hour)
		if _, err := rec.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	srv := newParityServer(t, ParityDeps{Store: store})
	cases := []struct {
		golden string
		args   map[string]any
	}{
		{"audit/all.json", nil},
		{"audit/filtered.json", map[string]any{"action": "config_change", "since": "2026-01-02T09:30:00Z", "until": "2026-01-02T12:00:00Z"}},
		{"audit/limited.json", map[string]any{"limit": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			var args any
			if tc.args != nil {
				args = tc.args
			}
			assertGolden(t, tc.golden, execTool(t, srv, "tokenops_audit", args))
		})
	}
}
