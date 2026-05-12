// Package audit provides an append-only log of governance-relevant
// TokenOps actions: config changes, optimization accepts/rejects,
// telemetry toggles, redaction-rule edits, etc. The log is backed by a
// dedicated audit_log table in the local SQLite store and is read-only
// by design — there is no Update or Delete API.
//
// The intent is operational: when "we accidentally enabled cloud
// telemetry last Thursday" surfaces, the audit table is the system of
// record. It is not a security log; high-volume signals (every prompt
// event) live in the events table.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

// Action enumerates the audit-able event types. New values append.
type Action string

// Known actions.
const (
	ActionConfigChange       Action = "config_change"
	ActionOptimizationAccept Action = "optimization_accept"
	ActionOptimizationReject Action = "optimization_reject"
	ActionTelemetryToggle    Action = "telemetry_toggle"
	ActionRedactionUpdate    Action = "redaction_update"
	ActionBudgetUpdate       Action = "budget_update"
	ActionBudgetExceeded     Action = "budget_exceeded"
	ActionOptimizationApply  Action = "optimization_apply"
	ActionDataExport         Action = "data_export"
)

// Entry is one row of the audit log. ID is a UUIDv4 minted by Record;
// callers can pass an empty ID and Record will fill it in.
type Entry struct {
	ID        string
	Timestamp time.Time
	Action    Action
	Actor     string
	Target    string
	Details   map[string]any
}

// Recorder writes entries to the local SQLite store. Construct via
// NewRecorder; the zero value is unusable.
type Recorder struct {
	store *sqlite.Store
}

// NewRecorder returns a Recorder backed by store.
func NewRecorder(store *sqlite.Store) *Recorder { return &Recorder{store: store} }

// Record appends entry. ID/Timestamp are populated when zero. Returns
// the persisted Entry so callers can surface the canonical timestamp.
func (r *Recorder) Record(ctx context.Context, entry Entry) (Entry, error) {
	if r == nil || r.store == nil {
		return Entry{}, errors.New("audit: recorder not initialised")
	}
	if entry.Action == "" {
		return Entry{}, errors.New("audit: action required")
	}
	if entry.Actor == "" {
		return Entry{}, errors.New("audit: actor required")
	}
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	var detailsJSON sql.NullString
	if len(entry.Details) > 0 {
		raw, err := json.Marshal(entry.Details)
		if err != nil {
			return Entry{}, fmt.Errorf("audit: marshal details: %w", err)
		}
		detailsJSON = sql.NullString{String: string(raw), Valid: true}
	}
	target := sql.NullString{}
	if entry.Target != "" {
		target = sql.NullString{String: entry.Target, Valid: true}
	}
	_, err := r.store.DB().ExecContext(ctx, insertSQL,
		entry.ID,
		entry.Timestamp.UTC().UnixNano(),
		string(entry.Action),
		entry.Actor,
		target,
		detailsJSON,
	)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: insert: %w", err)
	}
	return entry, nil
}

// Filter narrows audit-log queries. Empty fields are unconstrained;
// Limit <= 0 falls back to defaultQueryLimit.
type Filter struct {
	Action Action
	Actor  string
	Since  time.Time
	Until  time.Time
	Limit  int
}

const defaultQueryLimit = 1000

// Query returns matching entries ordered by Timestamp descending (newest
// first — what dashboards and CLI tables want).
func (r *Recorder) Query(ctx context.Context, f Filter) ([]Entry, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("audit: recorder not initialised")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	var (
		conds []string
		args  []any
	)
	if f.Action != "" {
		conds = append(conds, "action = ?")
		args = append(args, string(f.Action))
	}
	if f.Actor != "" {
		conds = append(conds, "actor = ?")
		args = append(args, f.Actor)
	}
	if !f.Since.IsZero() {
		conds = append(conds, "timestamp_ns >= ?")
		args = append(args, f.Since.UTC().UnixNano())
	}
	if !f.Until.IsZero() {
		conds = append(conds, "timestamp_ns < ?")
		args = append(args, f.Until.UTC().UnixNano())
	}
	q := selectSQL
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY timestamp_ns DESC LIMIT ?"
	args = append(args, limit)

	rows, err := r.store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entry
	for rows.Next() {
		var (
			e           Entry
			ts          int64
			actionStr   string
			target      sql.NullString
			detailsJSON sql.NullString
		)
		if err := rows.Scan(&e.ID, &ts, &actionStr, &e.Actor, &target, &detailsJSON); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		e.Timestamp = time.Unix(0, ts).UTC()
		e.Action = Action(actionStr)
		if target.Valid {
			e.Target = target.String
		}
		if detailsJSON.Valid {
			var d map[string]any
			if err := json.Unmarshal([]byte(detailsJSON.String), &d); err != nil {
				return nil, fmt.Errorf("audit: decode details: %w", err)
			}
			e.Details = d
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: rows: %w", err)
	}
	return out, nil
}

const insertSQL = `
INSERT INTO audit_log (id, timestamp_ns, action, actor, target, details)
VALUES (?, ?, ?, ?, ?, ?)
`

const selectSQL = `
SELECT id, timestamp_ns, action, actor, target, details
FROM audit_log
`
