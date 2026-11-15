package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// TestEachChangeIsCountedOnce pins the property the retention pass's cost
// rests on: a change to an event past the change log's horizon writes no
// log row and only bumps the single counter row, a change to a younger
// event writes the log and leaves the counter, and every change is counted
// by exactly one of the two. A timing budget would be flaky on shared CI
// runners; this fails deterministically if a large delete ever starts
// writing a row per deleted event.
func TestEachChangeIsCountedOnce(t *testing.T) {
	now := time.Now().UTC()
	const rows = 200
	cases := []struct {
		name      string
		age       time.Duration
		statement string
		wantLog   int
		wantOlder int
	}{
		{"retention delete of old events", 60 * 24 * time.Hour, `DELETE FROM events`, 0, rows},
		{"update of old events", 60 * 24 * time.Hour, `UPDATE events SET source = 'other'`, 0, rows},
		{"delete of young events", 24 * time.Hour, `DELETE FROM events`, rows, 0},
		{"update of young events", 24 * time.Hour, `UPDATE events SET source = 'other'`, rows, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			batch := make([]*eventschema.Envelope, 0, rows)
			for i := range rows {
				batch = append(batch, mustPromptEnvelope(t, fmt.Sprintf("e%d", i), now.Add(-tc.age).Add(time.Duration(i)*time.Second),
					&eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, InputTokens: 1}))
			}
			if err := s.AppendBatch(ctx, batch); err != nil {
				t.Fatalf("append: %v", err)
			}
			logBefore, olderBefore := changeCounts(t, s)
			if _, err := s.DB().ExecContext(ctx, tc.statement); err != nil {
				t.Fatalf("%s: %v", tc.statement, err)
			}
			logAfter, olderAfter := changeCounts(t, s)
			if got := logAfter - logBefore; got != tc.wantLog {
				t.Errorf("change log grew by %d rows, want %d", got, tc.wantLog)
			}
			if got := olderAfter - olderBefore; got != tc.wantOlder {
				t.Errorf("older-change counter moved by %d, want %d", got, tc.wantOlder)
			}
			var counterRows int
			if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events_older_changes`).Scan(&counterRows); err != nil {
				t.Fatalf("count counter rows: %v", err)
			}
			if counterRows != 1 {
				t.Errorf("older-change counter has %d rows, want 1", counterRows)
			}
		})
	}
}

// changeCounts is the change log's row count and the older-change counter.
func changeCounts(t *testing.T, s *Store) (logRows, older int) {
	t.Helper()
	err := s.DB().QueryRowContext(context.Background(),
		`SELECT (SELECT COUNT(*) FROM events_changes), (SELECT gen FROM events_older_changes WHERE id = 1)`).Scan(&logRows, &older)
	if err != nil {
		t.Fatalf("change counts: %v", err)
	}
	return logRows, older
}
