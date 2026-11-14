package sqlite

import (
	"context"
	"errors"
	"fmt"
)

// WorkflowMark is where a workflow's prompt events stood in the store:
// the rows they were, and the store's change log and newest rows then.
// WorkflowUnchanged tells from it whether any of them changed since, so
// what was derived from them can be kept (ADR 0019).
type WorkflowMark struct {
	seq  int64
	tail []tailRow
	rows []int64
}

// workflowRowsSQL lists a workflow's prompt events by row.
const workflowRowsSQL = `SELECT rowid FROM events WHERE type = 'prompt' AND workflow_id = ?`

// MarkWorkflow marks where workflow id's prompt events stand now. Read it
// before the events it marks: a change made between the two is then seen
// as a change, never missed. A store without a change log cannot be
// marked; the error says so.
func (s *Store) MarkWorkflow(ctx context.Context, id string) (WorkflowMark, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowMark{}, fmt.Errorf("sqlite: workflow mark: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	snap, err := snapshotOf(ctx, tx)
	if err != nil {
		return WorkflowMark{}, err
	}
	rs, err := tx.QueryContext(ctx, workflowRowsSQL, id)
	if err != nil {
		return WorkflowMark{}, fmt.Errorf("sqlite: workflow mark: %w", err)
	}
	defer func() { _ = rs.Close() }()
	m := WorkflowMark{seq: snap.seq, tail: snap.tail}
	for rs.Next() {
		var row int64
		if err := rs.Scan(&row); err != nil {
			return WorkflowMark{}, fmt.Errorf("sqlite: workflow mark: %w", err)
		}
		m.rows = append(m.rows, row)
	}
	return m, rs.Err()
}

// workflowRowPastSQL finds a workflow's prompt event past a rowid, by
// rowid (NOT INDEXED, as newEventsSQL).
const workflowRowPastSQL = `SELECT 1 FROM events NOT INDEXED
WHERE rowid > ? AND type = 'prompt' AND workflow_id = ? LIMIT 1`

// workflowRowChangedSQL finds a workflow's prompt event logged as changed
// after a log position.
const workflowRowChangedSQL = `SELECT 1 FROM events NOT INDEXED
WHERE rowid IN (SELECT row FROM events_changes WHERE seq > ?) AND type = 'prompt' AND workflow_id = ? LIMIT 1`

// WorkflowUnchanged reports whether workflow id's prompt events are still
// the ones m marked: none stored, updated or deleted since. It returns
// the mark moved to now, for the next check. It reports false when it
// cannot tell: the change log moved on past m, or rowids were renumbered.
func (s *Store) WorkflowUnchanged(ctx context.Context, id string, m WorkflowMark) (WorkflowMark, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m, false, fmt.Errorf("sqlite: workflow mark: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	snap, err := snapshotOf(ctx, tx)
	if err != nil {
		return m, false, err
	}
	if snap.seq > m.seq && (snap.firstSeq == 0 || snap.firstSeq > m.seq+1) {
		return m, false, nil
	}
	known, ok, err := knownThrough(ctx, tx, m.tail)
	if err != nil || !ok {
		return m, false, err
	}
	if found, err := exists(ctx, tx, workflowRowPastSQL, known, id); err != nil || found {
		return m, false, err
	}
	if snap.seq > m.seq {
		changed, err := changedRows(ctx, tx, m.seq)
		if err != nil {
			return m, false, err
		}
		for _, r := range m.rows {
			if _, logged := changed[r]; logged {
				return m, false, nil
			}
		}
		// A row that joined the workflow since.
		if found, err := exists(ctx, tx, workflowRowChangedSQL, m.seq, id); err != nil || found {
			return m, false, err
		}
	}
	return WorkflowMark{seq: snap.seq, tail: snap.tail, rows: m.rows}, true, nil
}

// exists reports whether query, a SELECT 1 … LIMIT 1, finds a row.
func exists(ctx context.Context, q querier, query string, args ...any) (bool, error) {
	rs, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("sqlite: workflow mark: %w", err)
	}
	defer func() { _ = rs.Close() }()
	found := rs.Next()
	return found, rs.Err()
}

// IsNoChangeLog reports whether err is a store's lack of a change log:
// what it marks cannot be checked later.
func IsNoChangeLog(err error) bool { return errors.Is(err, errNoChangeLog) }
