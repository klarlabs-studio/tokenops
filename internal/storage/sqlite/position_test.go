package sqlite

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A derived figure may be kept as long as the store has not moved
// (ADR 0019): a new event, an updated one and a deleted one each move it,
// and reading without changing anything does not.
func TestPositionMovesWithEveryChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	appendOne := func(id string) {
		t.Helper()
		env := mustPromptEnvelope(t, id, at, &eventschema.PromptEvent{
			PromptHash: "h-" + id, Provider: eventschema.ProviderAnthropic,
			RequestModel: "claude", InputTokens: 1, OutputTokens: 1,
		})
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	position := func() Position {
		t.Helper()
		p, err := s.Position(ctx)
		if err != nil {
			t.Fatalf("Position: %v", err)
		}
		return p
	}

	empty := position()
	appendOne("a")
	appended := position()
	if appended == empty {
		t.Fatalf("an appended event left the position at %+v", empty)
	}
	if _, err := s.Query(ctx, Filter{}); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := position(); got != appended {
		t.Errorf("a read moved the position from %+v to %+v", appended, got)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE events SET source = 'other'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated := position()
	if updated == appended {
		t.Errorf("an updated event left the position at %+v", appended)
	}
	appendOne("b")
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM events WHERE rowid = (SELECT MAX(rowid) FROM events)`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The newest row is gone, so the largest rowid is back where it was:
	// only the change log tells this store apart from the updated one.
	if got := position(); got == updated {
		t.Errorf("a deleted event left the position at %+v", updated)
	}
}
