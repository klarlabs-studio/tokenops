package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestOutcomeRecordPersistsHumanAssessment(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := NewServer("test", "test", nil)
	if err := RegisterOutcomeTools(srv, OutcomeDeps{Store: store}); err != nil {
		t.Fatal(err)
	}
	var got outcomeResult
	out := execTool(t, srv, "tokenops_outcome_record", map[string]any{
		"execution_id": "exec:1", "decision_id": "decision:1", "result": "achieved",
	})
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Recorded || got.Assessment != "human" {
		t.Fatalf("result = %+v", got)
	}
	events, err := store.Query(context.Background(), sqlite.Filter{Type: eventschema.EventTypeOutcome, Decision: "decision:1"})
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %d, err = %v", len(events), err)
	}
}
