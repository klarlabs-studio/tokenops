package planhistory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Every plan change lands in the audit log, not only a backdated one:
// a switch made now (by the operator or by TokenOps following a client)
// is the change an operator most needs to find again. The evidence that
// prompted it is recorded with it.
func TestRecordAuditsASwitchMadeNow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "events.db")
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if _, err := Record(ctx, Switch{
		Provider: "anthropic", Previous: "claude-ultra", Plan: "claude-max", From: now, Now: now,
		DBPath: db, Actor: "claude_code",
		Evidence: map[string]any{"account_type": "claude_max", "rate_limit_tier": "default_claude_max_5x"},
	}); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	entries, err := audit.NewRecorder(store).Query(ctx, audit.Filter{Action: audit.ActionPlanChange})
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit = %+v, %v", entries, err)
	}
	e := entries[0]
	evidence, _ := e.Details["evidence"].(map[string]any)
	if e.Actor != "claude_code" || e.Target != "anthropic" || e.Details["previous"] != "claude-ultra" ||
		e.Details["plan"] != "claude-max" || evidence["rate_limit_tier"] != "default_claude_max_5x" {
		t.Errorf("entry = %+v", e)
	}
}

// An open store is used as given, so the daemon audits through the
// store it already holds instead of opening a second one.
func TestRecordAuditsThroughAGivenStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if _, err := Record(ctx, Switch{Provider: "openai", Plan: "gpt-pro-5x", From: now.Add(-time.Hour), Now: now, Store: store, Actor: "codex"}); err != nil {
		t.Fatal(err)
	}
	entries, err := audit.NewRecorder(store).Query(ctx, audit.Filter{Action: audit.ActionPlanChange})
	if err != nil || len(entries) != 1 || entries[0].Actor != "codex" {
		t.Fatalf("audit = %+v, %v", entries, err)
	}
}

func TestReportsRoundTrip(t *testing.T) {
	f := Reports{Path: filepath.Join(t.TempDir(), "x", "plan-reports.json")}
	if got, err := f.Load(); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v", got, err)
	}
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	want := map[string]Report{"anthropic": {Plan: "claude-max", Client: "claude_code", Detail: "account type claude_max", FirstSeen: at}}
	if err := f.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := f.Load()
	if err != nil || got["anthropic"].Plan != "claude-max" || !got["anthropic"].FirstSeen.Equal(at) {
		t.Fatalf("Load = %+v, %v", got, err)
	}
}
