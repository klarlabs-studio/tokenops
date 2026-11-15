package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Position moves for a change to an event of any age (ADR 0019): the
// change log holds only the changes to events younger than the horizon,
// so the older ones must move the position another way. Each case starts
// from a store holding the target event and some older rows, then makes
// one change; a young anchor row holds the newest rowid so that only the
// change under test can move the position.
func TestPositionMovesForChangesAtAnyAge(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	const target = "target"
	cases := []struct {
		name   string
		age    time.Duration
		change func(t *testing.T, s *Store)
	}{
		{"insert of a young event", 24 * time.Hour, nil},
		{"update of a young event", 24 * time.Hour, updateEvent(target)},
		{"delete of a young event", 24 * time.Hour, deleteEvent(target)},
		{"insert of an old event", 60 * 24 * time.Hour, nil},
		{"update of an old event", 60 * 24 * time.Hour, updateEvent(target)},
		{"delete of an old event", 60 * 24 * time.Hour, deleteEvent(target)},
		{"retention prune of old events", 60 * 24 * time.Hour, pruneOlderThan(now.Add(-50 * 24 * time.Hour))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			at := now.Add(-tc.age)
			seed := func() {
				t.Helper()
				env := mustPromptEnvelope(t, target, at, &eventschema.PromptEvent{PromptHash: "h", Provider: eventschema.ProviderAnthropic, InputTokens: 1})
				if err := s.Append(ctx, env); err != nil {
					t.Fatalf("append: %v", err)
				}
			}
			if tc.change != nil {
				seed()
				// A prune needs several old rows to remove.
				for i := range 5 {
					env := mustPromptEnvelope(t, fmt.Sprintf("old-%d", i), at.Add(time.Duration(i)*time.Minute), &eventschema.PromptEvent{PromptHash: "h", Provider: eventschema.ProviderAnthropic, InputTokens: 1})
					if err := s.Append(ctx, env); err != nil {
						t.Fatalf("append: %v", err)
					}
				}
			}
			anchor := mustPromptEnvelope(t, "anchor", now.Add(-time.Hour), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
			if err := s.Append(ctx, anchor); err != nil {
				t.Fatalf("append anchor: %v", err)
			}
			before := mustPosition(t, s)
			if tc.change == nil {
				seed()
			} else {
				tc.change(t, s)
			}
			if got := mustPosition(t, s); got == before {
				t.Errorf("position stayed at %+v", before)
			}
		})
	}
}

func mustPosition(t *testing.T, s *Store) Position {
	t.Helper()
	p, err := s.Position(context.Background())
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	return p
}

func updateEvent(id string) func(t *testing.T, s *Store) {
	return func(t *testing.T, s *Store) {
		t.Helper()
		if _, err := s.DB().ExecContext(context.Background(), `UPDATE events SET source = 'other' WHERE id = ?`, id); err != nil {
			t.Fatalf("update: %v", err)
		}
	}
}

func deleteEvent(id string) func(t *testing.T, s *Store) {
	return func(t *testing.T, s *Store) {
		t.Helper()
		if _, err := s.DB().ExecContext(context.Background(), `DELETE FROM events WHERE id = ?`, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
}

func pruneOlderThan(cutoff time.Time) func(t *testing.T, s *Store) {
	return func(t *testing.T, s *Store) {
		t.Helper()
		if _, err := s.DB().ExecContext(context.Background(), `DELETE FROM events WHERE timestamp_ns < ?`, cutoff.UnixNano()); err != nil {
			t.Fatalf("prune: %v", err)
		}
	}
}

// BenchmarkRetentionPrune is what a retention pass costs on a store whose
// oldest 100,000 events are all past the change log's horizon: one delete
// statement over them. The "without older counter" variant drops the
// migration's counter triggers, so the difference is what they cost.
func BenchmarkRetentionPrune(b *testing.B) {
	for _, v := range []struct {
		name    string
		counter bool
	}{{"with older counter", true}, {"without older counter", false}} {
		b.Run(v.name, func(b *testing.B) {
			ctx := context.Background()
			const rows = 100000
			base := time.Now().UTC().Add(-120 * 24 * time.Hour)
			for range b.N {
				b.StopTimer()
				s := newTestStore(b)
				if !v.counter {
					for _, q := range []string{`DROP TRIGGER events_older_changes_on_update`, `DROP TRIGGER events_older_changes_on_delete`} {
						if _, err := s.DB().ExecContext(ctx, q); err != nil {
							b.Fatal(err)
						}
					}
				}
				batch := make([]*eventschema.Envelope, 0, 1000)
				for i := range rows {
					batch = append(batch, mustPromptEnvelope(b, fmt.Sprintf("p%d", i), base.Add(time.Duration(i)*time.Second), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, InputTokens: 1}))
					if len(batch) == cap(batch) {
						if err := s.AppendBatch(ctx, batch); err != nil {
							b.Fatal(err)
						}
						batch = batch[:0]
					}
				}
				cutoff := base.Add(rows * time.Second)
				b.StartTimer()
				if _, err := s.DB().ExecContext(ctx, `DELETE FROM events WHERE timestamp_ns < ?`, cutoff.UnixNano()); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
			}
		})
	}
}
