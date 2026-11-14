package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"time"

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

// ChangeLogHorizon is how old an event may be for an update or delete of
// it to move Seq: a change to an older event is not logged (see
// eventsChangesMigration), so a figure that reads further back than this
// digests that part of its read (SpanDigest) beside the position.
const ChangeLogHorizon = changeLogHorizon

// SpanDigest is a digest of the events of type t in [since, until), from
// the columns events_usage_idx holds (timestamp, provider, model, source,
// tokens, cost and cost source), read from that index alone. An insert or
// delete in the span changes it, and so does every update the store makes
// to an event (re-attribution, a plan restamp, the Codex output
// correction), whatever its age. It is what tells a figure that reads past
// ChangeLogHorizon that an event it read there changed, which Position
// does not.
//
// Payload and attribute fields outside the index are not in it; no update
// the store makes changes one of those alone.
func (s *Store) SpanDigest(ctx context.Context, t eventschema.EventType, since, until time.Time) (string, error) {
	rows, err := s.db.QueryContext(ctx, spanDigestSQL, string(t), since.UTC().UnixNano(), until.UTC().UnixNano())
	if err != nil {
		return "", fmt.Errorf("sqlite: span digest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var (
		ts             int64
		text           [4]sql.RawBytes // provider, model, source, cost source
		in, out, total sql.NullInt64
		cost           sql.NullFloat64
		n              int64
		sum            uint64
		buf            []byte
	)
	h := fnv.New64a()
	for rows.Next() {
		if err := rows.Scan(&ts, &text[0], &text[1], &text[2], &in, &out, &total, &cost, &text[3]); err != nil {
			return "", fmt.Errorf("sqlite: span digest: %w", err)
		}
		buf = binary.LittleEndian.AppendUint64(buf[:0], uint64(ts))
		for _, v := range []sql.NullInt64{in, out, total} {
			buf = appendNullable(buf, v.Valid, uint64(v.Int64))
		}
		buf = appendNullable(buf, cost.Valid, math.Float64bits(cost.Float64))
		for _, c := range text {
			// A NULL reads as nil, an empty text as empty: told apart.
			if c == nil {
				buf = append(buf, 0)
				continue
			}
			buf = binary.AppendUvarint(append(buf, 1), uint64(len(c)))
			buf = append(buf, c...)
		}
		h.Reset()
		_, _ = h.Write(buf)
		// A sum of the rows' hashes: the same events give the same digest
		// in whatever order they are read.
		sum += h.Sum64()
		n++
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("sqlite: span digest: %w", err)
	}
	return fmt.Sprintf("%d.%016x", n, sum), nil
}

// appendNullable appends a nullable value's bits to b, NULL told apart.
func appendNullable(b []byte, valid bool, bits uint64) []byte {
	if !valid {
		return append(b, 0)
	}
	return binary.LittleEndian.AppendUint64(append(b, 1), bits)
}

// spanDigestSQL reads what SpanDigest digests. The cost source expression
// is the index's own, which the planner only matches written the same way.
const spanDigestSQL = `SELECT timestamp_ns, provider, model, source, input_tokens, output_tokens, total_tokens, cost_usd,
		COALESCE(json_extract(payload, '$.cost_source'), '')
	FROM events INDEXED BY events_usage_idx
	WHERE type = ? AND timestamp_ns >= ? AND timestamp_ns < ?`
