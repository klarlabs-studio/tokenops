package workflow

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Reconstructing a trace is reading it. It published workflow.observed on
// every call, so each view stored a domain event and the counters counted
// views of a workflow as if they were the workflow.
func TestReconstructWritesNothing(t *testing.T) {
	store := newStore(t)
	now := time.Now().UTC()
	for i := range 3 {
		env := mkStep(
			"e"+strconv.Itoa(i), "wf-1", "agent-x", "gpt-4",
			now.Add(time.Duration(i)*time.Second), 100, 20, 0.001, 200*time.Millisecond,
		)
		if err := store.Append(context.Background(), env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	for range 2 {
		if _, err := Reconstruct(context.Background(), store, spend.NewEngine(spend.DefaultTable()), "wf-1"); err != nil {
			t.Fatalf("reconstruct: %v", err)
		}
	}
	got, err := store.Query(context.Background(), sqlite.Filter{Type: eventschema.EventTypeDomain})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("reading a trace stored %d domain events", len(got))
	}
}
