package sqlite

import (
	"context"
	"fmt"
)

// Position is where the event store stands: equal positions mean no event
// was added, updated or deleted between them (ADR 0019). A job that keeps
// a figure derived from events compares positions to know whether it has
// to compute the figure again.
//
// Rowid moves with every insert. Seq is the latest events_changes entry
// ever written, which moves with every update and delete, including the
// delete of the newest row that leaves the next insert free to reuse its
// rowid. A VACUUM may renumber rows without either changing anything a
// figure reads; at worst it moves Rowid down and costs one recomputation.
type Position struct {
	Rowid int64
	Seq   int64
}

// Position is the store's current position. Two lookups, each answered
// from the end of an index or a one-row table.
func (s *Store) Position(ctx context.Context) (Position, error) {
	var p Position
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT MAX(rowid) FROM events), 0),
		COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'events_changes'), 0)`).Scan(&p.Rowid, &p.Seq)
	if err != nil {
		return Position{}, fmt.Errorf("sqlite: store position: %w", err)
	}
	return p, nil
}
