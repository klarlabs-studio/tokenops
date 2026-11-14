package sqlite

import (
	"context"
	"fmt"
	"strconv"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// repeatMarker is eventschema.AttrRepeat as it appears in a stored
// attributes column.
var repeatMarker = strconv.Quote(eventschema.AttrRepeat) + ":" + strconv.Quote(eventschema.AttrRepeatValue)

// Position is where the event store stands: equal positions mean no event
// was added, updated or deleted between them (ADR 0019). A job that keeps
// a figure derived from events compares positions to know whether it has
// to compute the figure again.
//
// Rowid moves with every insert but one: a vendor reading stored again
// unchanged (eventschema.AttrRepeat), whose figures are the reading
// before it. Its reader stores it every ten minutes while the account is
// idle, only so the newest reading's age stays honest; counting it moved
// every kept figure about every six minutes on a machine nobody used.
// What a repeat does change is time-only (a reading's observed time, which
// of two sources' readings is newer), and a kept figure is recomputed at
// its max age anyway. Seq is the latest events_changes entry
// ever written, which moves with every update and delete, including the
// delete of the newest row that leaves the next insert free to reuse its
// rowid. A VACUUM may renumber rows without either changing anything a
// figure reads; at worst it moves Rowid down and costs one recomputation.
type Position struct {
	Rowid int64
	Seq   int64
}

// Position is the store's current position. Two lookups: the newest row
// that is not a repeat, read back from the end of the table (past the
// repeats stored since, a few an hour), and a one-row table.
//
// The marker is matched as text, as encoding/json writes it: a value that
// contains the same characters is escaped, so it never matches.
func (s *Store) Position(ctx context.Context) (Position, error) {
	var p Position
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT rowid FROM events
			WHERE attributes IS NULL OR instr(attributes, ?) = 0
			ORDER BY rowid DESC LIMIT 1), 0),
		COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'events_changes'), 0)`, repeatMarker).Scan(&p.Rowid, &p.Seq)
	if err != nil {
		return Position{}, fmt.Errorf("sqlite: store position: %w", err)
	}
	return p, nil
}
