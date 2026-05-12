package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/security/audit"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

func TestAuditAPIReturnsEntries(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rec := audit.NewRecorder(store)
	if _, err := rec.Record(context.Background(), audit.Entry{
		Action:    audit.ActionBudgetExceeded,
		Actor:     "test",
		Target:    "weekly",
		Timestamp: time.Now().UTC(),
		Details:   map[string]any{"spent_usd": 150.0, "limit_usd": 100.0},
	}); err != nil {
		t.Fatal(err)
	}
	h := NewAuditHandlers(store)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/audit")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		Entries []audit.Entry `json:"entries"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.Entries))
	}
	if got.Entries[0].Action != audit.ActionBudgetExceeded {
		t.Errorf("action = %q", got.Entries[0].Action)
	}
}

func TestAuditAPIFilterByAction(t *testing.T) {
	store, _ := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"), sqlite.Options{})
	t.Cleanup(func() { _ = store.Close() })
	rec := audit.NewRecorder(store)
	for _, a := range []audit.Action{audit.ActionBudgetExceeded, audit.ActionOptimizationApply} {
		_, _ = rec.Record(context.Background(), audit.Entry{Action: a, Actor: "t", Timestamp: time.Now().UTC()})
	}
	h := NewAuditHandlers(store)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/audit?action=optimization_apply")
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		Entries []audit.Entry `json:"entries"`
	}
	_ = json.Unmarshal(body, &got)
	if len(got.Entries) != 1 || got.Entries[0].Action != audit.ActionOptimizationApply {
		t.Errorf("filter broken: %+v", got.Entries)
	}
}
