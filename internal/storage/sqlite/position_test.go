package sqlite

import (
	"context"
	"fmt"
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

// A reading stored again unchanged (eventschema.AttrRepeat) changes no
// figure computed from the readings' contents, so it does not move the
// position; a reading with new figures does, and so does deleting a
// repeat, which the change log records like any delete.
func TestPositionStandsStillForARepeatedReading(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	reading := func(id string, attrs map[string]string) {
		t.Helper()
		env := mustPromptEnvelope(t, id, at, &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, Status: 200})
		env.Attributes = attrs
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		at = at.Add(10 * time.Minute)
	}
	position := func() Position {
		t.Helper()
		p, err := s.Position(ctx)
		if err != nil {
			t.Fatalf("Position: %v", err)
		}
		return p
	}
	figures := map[string]string{"granularity": "quota_snapshot", "primary_used_pct": "40.00"}
	repeat := map[string]string{"granularity": "quota_snapshot", "primary_used_pct": "40.00", eventschema.AttrRepeat: eventschema.AttrRepeatValue}
	// A value that merely contains the marker's text is not the marker.
	lookalike := map[string]string{"granularity": "quota_snapshot", "note": `"repeat":"true"`}

	reading("r1", figures)
	first := position()
	reading("r2", repeat)
	reading("r3", repeat)
	if got := position(); got != first {
		t.Fatalf("repeated readings moved the position from %+v to %+v", first, got)
	}
	reading("r4", lookalike)
	moved := position()
	if moved == first {
		t.Fatalf("a reading with new figures left the position at %+v", first)
	}
	reading("r5", repeat)
	if got := position(); got != moved {
		t.Errorf("a repeat after a new reading moved the position from %+v to %+v", moved, got)
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM events WHERE id = 'r5'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := position(); got == moved {
		t.Errorf("deleting a repeat left the position at %+v", moved)
	}
}

// The repeats the position steps over are read from the end of the table
// and stop at the first other row, however many there are.
func BenchmarkPositionAfterRepeats(b *testing.B) {
	s := newTestStore(b)
	ctx := context.Background()
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	for i := range 2000 {
		env := mustPromptEnvelope(b, fmt.Sprintf("e%d", i), at, &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, Status: 200})
		if i >= 1000 {
			env.Attributes = map[string]string{"granularity": "quota_snapshot", eventschema.AttrRepeat: eventschema.AttrRepeatValue}
		}
		if err := s.Append(ctx, env); err != nil {
			b.Fatal(err)
		}
		at = at.Add(time.Minute)
	}
	for b.Loop() {
		if _, err := s.Position(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
