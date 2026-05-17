package tasks

import (
	"context"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

// Metrics is the per-task rollup over the events the operator's
// agent emitted while the task was open. Computed by querying the
// events.db for prompt events that fall in [Task.StartedAt,
// Task.CompletedAt]; open tasks use now as the upper bound.
type Metrics struct {
	Turns        int64         `json:"turns"`
	InputTokens  int64         `json:"input_tokens"`
	OutputTokens int64         `json:"output_tokens"`
	CostUSD      float64       `json:"cost_usd"`
	TTFUOSeconds float64       `json:"ttfuo_seconds"` // time from task start to first assistant turn
	Duration     time.Duration `json:"duration_ns"`
	CostPerTurn  float64       `json:"cost_per_turn"`
}

// MetricsFor computes Metrics for a single Task by querying the
// sqlite store. When CompletedAt is zero (open task) the upper
// bound defaults to clock(). Returns zero-valued Metrics when no
// prompts fall in the window — callers render that as "no
// activity".
func MetricsFor(ctx context.Context, store *sqlite.Store, t Task, clock func() time.Time) (Metrics, error) {
	if clock == nil {
		clock = time.Now
	}
	until := t.CompletedAt
	if until.IsZero() {
		until = clock().UTC()
	}
	var m Metrics
	m.Duration = until.Sub(t.StartedAt)
	if store == nil {
		return m, nil
	}
	rows, err := store.DB().QueryContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cost_usd),0),
			MIN(timestamp_ns)
		FROM events
		WHERE type = 'prompt'
		  AND timestamp_ns >= ? AND timestamp_ns <= ?
	`, t.StartedAt.UnixNano(), until.UnixNano())
	if err != nil {
		return m, err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var firstTs *int64
		if err := rows.Scan(&m.Turns, &m.InputTokens, &m.OutputTokens, &m.CostUSD, &firstTs); err != nil {
			return m, err
		}
		if firstTs != nil && *firstTs > 0 {
			first := time.Unix(0, *firstTs)
			m.TTFUOSeconds = first.Sub(t.StartedAt).Seconds()
			if m.TTFUOSeconds < 0 {
				m.TTFUOSeconds = 0
			}
		}
	}
	if m.Turns > 0 {
		m.CostPerTurn = m.CostUSD / float64(m.Turns)
	}
	return m, nil
}
