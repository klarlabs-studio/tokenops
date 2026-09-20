package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func assocStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "e.db"), Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func assocEnvelope(id string, a eventschema.Association) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID:            id,
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC(),
		Source:        "proxy",
		Association:   a,
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-6",
			InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		},
	}
}

// An association that does not survive the store is no association at
// all: everything downstream reads events back out of sqlite, so a
// field that only exists in memory attributes nothing to anything.
func TestAssociationSurvivesTheStore(t *testing.T) {
	s := assocStore(t)
	want := eventschema.Association{Work: "work:t1", Execution: "exec:t1", Actor: "session:abc"}

	if err := s.Append(context.Background(), assocEnvelope("e1", want)); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.Query(context.Background(), Filter{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].Association != want {
		t.Errorf("association = %+v, want %+v", got[0].Association, want)
	}
}

// An unassociated event reads back unassociated rather than as
// belonging to an empty work. The distinction is the whole point: "we
// do not know what this was for" is not "this was for nothing".
func TestAnUnassociatedEventStaysUnassociated(t *testing.T) {
	s := assocStore(t)
	if err := s.Append(context.Background(), assocEnvelope("e1", eventschema.Association{})); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.Query(context.Background(), Filter{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got[0].Associated() {
		t.Errorf("an unassociated event read back associated: %+v", got[0].Association)
	}
}

// Partial association round-trips. The proxy may know the actor from a
// header long before anything knows the goal.
func TestPartialAssociationSurvivesTheStore(t *testing.T) {
	s := assocStore(t)
	want := eventschema.Association{Actor: "session:abc"}
	if err := s.Append(context.Background(), assocEnvelope("e1", want)); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.Query(context.Background(), Filter{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got[0].Association != want {
		t.Errorf("association = %+v, want %+v", got[0].Association, want)
	}
}

// Querying by work is the point of storing it in columns rather than
// burying it in the payload: rolling an attempt's consumption up to the
// goal it served has to be a query, not a scan.
func TestEventsCanBeQueriedByWork(t *testing.T) {
	s := assocStore(t)
	ctx := context.Background()
	for _, e := range []*eventschema.Envelope{
		assocEnvelope("e1", eventschema.Association{Work: "work:a", Execution: "exec:1"}),
		assocEnvelope("e2", eventschema.Association{Work: "work:a", Execution: "exec:2"}),
		assocEnvelope("e3", eventschema.Association{Work: "work:b", Execution: "exec:3"}),
		assocEnvelope("e4", eventschema.Association{}),
	} {
		if err := s.Append(ctx, e); err != nil {
			t.Fatalf("append %s: %v", e.ID, err)
		}
	}

	got, err := s.Query(ctx, Filter{Work: "work:a", Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events for work:a, got %d", len(got))
	}
	for _, e := range got {
		if e.Association.Work != "work:a" {
			t.Errorf("query returned %q", e.Association.Work)
		}
	}
}

// Filtering by execution is what makes two attempts at the same goal
// comparable, which is what every experiment in Phase 5 needs.
func TestEventsCanBeQueriedByExecution(t *testing.T) {
	s := assocStore(t)
	ctx := context.Background()
	for _, e := range []*eventschema.Envelope{
		assocEnvelope("e1", eventschema.Association{Work: "work:a", Execution: "exec:1"}),
		assocEnvelope("e2", eventschema.Association{Work: "work:a", Execution: "exec:2"}),
	} {
		if err := s.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := s.Query(ctx, Filter{Execution: "exec:2", Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 || got[0].ID != "e2" {
		t.Fatalf("want only e2, got %+v", got)
	}
}

// A store created before these columns existed must open, migrate and
// keep its rows. The migration runs on every installed machine that has
// ever written an event.
func TestAnExistingStoreMigratesAndKeepsItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.db")
	ctx := context.Background()

	first, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := first.Append(ctx, assocEnvelope("e1", eventschema.Association{})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening runs any pending migrations against the existing file.
	second, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	got, err := second.Query(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 || got[0].ID != "e1" {
		t.Fatalf("the migration lost rows: %+v", got)
	}
}
