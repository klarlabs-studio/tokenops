package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// codexSource is the source the Codex session-file poller records under
// (codexjsonl.SourceTag; storage does not import the poller).
const codexSource = "codex-jsonl"

// CorrectCodexOutput takes the reasoning tokens that Codex turns recorded
// by releases up to v0.118.18 counted twice back out of their output and
// total, and returns how many turns it corrected. It walks the Codex
// turns batch at a time, each batch its own short write, so it never
// holds the write lock other processes wait on for long; a cancelled
// context stops it between batches and the next pass carries on.
//
// Codex's output_tokens already includes its reasoning_output_tokens.
// Those releases added the reasoning on top, to output_tokens and
// total_tokens, and kept the reasoning in the reasoning_output attribute,
// so subtracting that attribute gives Codex's own figure back exactly.
//
// Only a turn with the old poller's shape changes: no parser_version
// attribute (the fixed poller writes "2"), a whole-number reasoning_output
// no larger than the output, total equal to input + output, and payload
// and column agreeing on the output. Each corrected turn gets
// parser_version "2", so a second pass changes nothing. Input, cached
// input and everything else stay as they were.
//
// Codex turns store no cost (spend prices their tokens when read), so the
// correction reprices them by itself. A cost priced from the rate card
// would be stale, so it is cleared to be priced on read, as
// RestampPlanIncluded does; a cost the recorder stated (cost_measured) is
// what was billed and is kept.
func (s *Store) CorrectCodexOutput(ctx context.Context, batch int) (int64, error) {
	if batch <= 0 {
		return 0, fmt.Errorf("sqlite: codex output correction: batch must be positive, got %d", batch)
	}
	var total int64
	afterTS, afterRow := int64(math.MinInt64), int64(math.MinInt64)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		ids, lastTS, lastRow, err := s.codexBatch(ctx, afterTS, afterRow, batch)
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		res, err := s.db.ExecContext(ctx, correctCodexOutputSQL, string(ids))
		if err != nil {
			return total, fmt.Errorf("sqlite: codex output correction: %w", contended(ctx, err))
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("sqlite: codex output correction: %w", err)
		}
		total += n
		afterTS, afterRow = lastTS, lastRow
	}
}

// codexBatch reads the rowids of the next batch Codex turns after
// (afterTS, afterRow) in (timestamp_ns, rowid) order, from
// events_source_ts_idx alone, as a JSON array, and the position of the
// last one.
func (s *Store) codexBatch(ctx context.Context, afterTS, afterRow int64, batch int) (ids []byte, lastTS, lastRow int64, err error) {
	rows, err := s.db.QueryContext(ctx, codexBatchSQL, codexSource, afterTS, afterRow, batch)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("sqlite: codex output batch: %w", contended(ctx, err))
	}
	defer func() { _ = rows.Close() }()
	var list []int64
	for rows.Next() {
		if err := rows.Scan(&lastTS, &lastRow); err != nil {
			return nil, 0, 0, fmt.Errorf("sqlite: codex output batch: %w", err)
		}
		list = append(list, lastRow)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("sqlite: codex output batch: %w", contended(ctx, err))
	}
	if len(list) == 0 {
		return nil, 0, 0, nil
	}
	ids, err = json.Marshal(list)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("sqlite: codex output batch: %w", err)
	}
	return ids, lastTS, lastRow, nil
}

// codexBatchSQL walks a source's events in (timestamp_ns, rowid) order.
const codexBatchSQL = `SELECT timestamp_ns, rowid FROM events INDEXED BY events_source_ts_idx
WHERE source = ? AND (timestamp_ns, rowid) > (?, ?)
ORDER BY timestamp_ns, rowid LIMIT ?`

// correctCodexOutputSQL corrects the turns of one batch (a JSON array of
// rowids) that have the old poller's shape; see CorrectCodexOutput. The
// unary + keeps SQLite from answering source = ? through
// events_source_ts_idx, which reads every Codex turn for each batch,
// instead of looking up the batch's rowids (TestCorrectCodexOutputPlan).
var correctCodexOutputSQL = strings.NewReplacer(
	"{r}", "CAST(json_extract(attributes, '$.reasoning_output') AS INTEGER)",
	"{measured}", "coalesce(json_extract(payload, '$.cost_measured'), 0) <> 0",
).Replace(`
UPDATE events
SET output_tokens = nullif(coalesce(output_tokens, 0) - {r}, 0),
    total_tokens  = nullif(coalesce(total_tokens, 0) - {r}, 0),
    cost_usd      = CASE WHEN {measured} THEN cost_usd END,
    payload       = CASE WHEN {measured}
        THEN json_set(payload,
            '$.output_tokens', coalesce(output_tokens, 0) - {r},
            '$.total_tokens',  coalesce(total_tokens, 0) - {r})
        ELSE json_remove(json_set(payload,
            '$.output_tokens', coalesce(output_tokens, 0) - {r},
            '$.total_tokens',  coalesce(total_tokens, 0) - {r}), '$.cost_usd')
        END,
    attributes    = json_set(attributes, '$.parser_version', '2')
WHERE rowid IN (SELECT value FROM json_each(?))
  AND +source = '` + codexSource + `' AND +type = 'prompt'
  AND json_extract(attributes, '$.parser_version') IS NULL
  AND json_extract(attributes, '$.reasoning_output') = printf('%d', {r})
  AND {r} >= 0
  AND coalesce(output_tokens, 0) >= {r}
  AND coalesce(json_extract(payload, '$.output_tokens'), 0) = coalesce(output_tokens, 0)
  AND coalesce(total_tokens, 0) = coalesce(input_tokens, 0) + coalesce(output_tokens, 0)
`)
