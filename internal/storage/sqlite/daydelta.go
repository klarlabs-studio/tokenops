package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EachEventBetween hands fn every event of type t in [from, until), oldest
// first, one page in memory at a time. It is EachEvent bounded above, for
// a caller that keeps one day of events at a time (ADR 0019).
func (s *Store) EachEventBetween(ctx context.Context, t eventschema.EventType, from, until time.Time, fn func(*eventschema.Envelope) error) error {
	// The hint keeps the planner on the type and time index: a bounded
	// range of one hour must not walk the whole table after it.
	sel := strings.Replace(selectSQL, "FROM events", "FROM events INDEXED BY events_type_ts_idx", 1)
	return readPages(ctx, s.db, sel, []string{"type = ?", "timestamp_ns < ?"},
		[]any{string(t), until.UTC().UnixNano()}, from.UTC().UnixNano()-1,
		func(rs *sql.Rows) (*eventschema.Envelope, error) {
			env, err := scanEnvelope(rs)
			if err != nil {
				return nil, err
			}
			return env, fn(env)
		})
}

// ChangedTimes is the event times of type t that changed between from and
// to: the events inserted by rowid after from, and the events updated by
// the change log between from's sequence and to's. attributable is false
// when a change cannot be placed in time, and the caller must then take
// every day as changed: a deleted event (its time is gone), a change log
// that no longer holds every entry (trimmed, or its count disagrees with
// the sequence), or a position that went backwards.
//
// Changes the log does not hold (an update or delete of an event older
// than ChangeLogHorizon) are not reported here; SpanDigest covers them.
func (s *Store) ChangedTimes(ctx context.Context, t eventschema.EventType, from, to Position) ([]time.Time, bool, error) {
	if to.Rowid < from.Rowid || to.Seq < from.Seq {
		return nil, false, nil
	}
	var counted int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events_changes WHERE seq > ? AND seq <= ?`, from.Seq, to.Seq).Scan(&counted); err != nil {
		return nil, false, fmt.Errorf("sqlite: changed times: %w", err)
	}
	if counted != to.Seq-from.Seq {
		return nil, false, nil
	}
	// The type is checked in Go: a type condition lets the planner walk the
	// type's index, not the rows above from.Rowid, which are all there is.
	rows, err := s.db.QueryContext(ctx,
		`SELECT timestamp_ns, type FROM events WHERE rowid > ? AND rowid <= ?`, from.Rowid, to.Rowid)
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: changed times: %w", err)
	}
	out, err := scanTimes(rows, t)
	if err != nil {
		return nil, false, err
	}
	// A LEFT JOIN: a logged row that is gone comes back with NULLs.
	rows, err = s.db.QueryContext(ctx, `SELECT e.timestamp_ns, e.type
		FROM events_changes c LEFT JOIN events e ON e.rowid = c.row
		WHERE c.seq > ? AND c.seq <= ?`, from.Seq, to.Seq)
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: changed times: %w", err)
	}
	var gone bool
	logged, err := scanChanged(rows, t, &gone)
	if err != nil {
		return nil, false, err
	}
	out = append(out, logged...)
	return out, !gone, nil
}

// scanTimes appends the timestamps of rows of type t to out and closes rows.
func scanTimes(rows *sql.Rows, t eventschema.EventType) ([]time.Time, error) {
	defer func() { _ = rows.Close() }()
	var out []time.Time
	for rows.Next() {
		var ts int64
		var typ string
		if err := rows.Scan(&ts, &typ); err != nil {
			return nil, fmt.Errorf("sqlite: changed times: %w", err)
		}
		if typ == string(t) {
			out = append(out, time.Unix(0, ts).UTC())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: changed times: %w", err)
	}
	return out, nil
}

// scanChanged appends the times of logged changes to events of type t, and
// sets gone when a logged row no longer exists.
func scanChanged(rows *sql.Rows, t eventschema.EventType, gone *bool) ([]time.Time, error) {
	defer func() { _ = rows.Close() }()
	var out []time.Time
	for rows.Next() {
		var ts *int64
		var typ *string
		if err := rows.Scan(&ts, &typ); err != nil {
			return nil, fmt.Errorf("sqlite: changed times: %w", err)
		}
		switch {
		case ts == nil || typ == nil:
			*gone = true
		case *typ == string(t):
			out = append(out, time.Unix(0, *ts).UTC())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: changed times: %w", err)
	}
	return out, nil
}
