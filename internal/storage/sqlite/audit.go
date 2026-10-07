package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
)

// *Store is the audit log's persistence: it implements audit.Store over the
// audit_log table its schema creates.
var _ audit.Store = (*Store)(nil)

const auditInsertSQL = `
INSERT INTO audit_log (id, timestamp_ns, action, actor, target, details)
VALUES (?, ?, ?, ?, ?, ?)
`

const auditSelectSQL = `
SELECT id, timestamp_ns, action, actor, target, details
FROM audit_log
`

// AppendAudit implements audit.Store: it inserts entry, with an empty
// Target and empty Details stored as NULL.
func (s *Store) AppendAudit(ctx context.Context, entry audit.Entry) error {
	if s == nil {
		return audit.ErrNotInitialised
	}
	var detailsJSON sql.NullString
	if len(entry.Details) > 0 {
		raw, err := json.Marshal(entry.Details)
		if err != nil {
			return fmt.Errorf("audit: marshal details: %w", err)
		}
		detailsJSON = sql.NullString{String: string(raw), Valid: true}
	}
	target := sql.NullString{}
	if entry.Target != "" {
		target = sql.NullString{String: entry.Target, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, auditInsertSQL,
		entry.ID,
		entry.Timestamp.UTC().UnixNano(),
		string(entry.Action),
		entry.Actor,
		target,
		detailsJSON,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}

// QueryAudit implements audit.Store: the entries f selects, newest first,
// at most f.Limit of them (all of them when f.Limit <= 0).
func (s *Store) QueryAudit(ctx context.Context, f audit.Filter) ([]audit.Entry, error) {
	if s == nil {
		return nil, audit.ErrNotInitialised
	}
	q, args := auditQuery(f)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []audit.Entry
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: rows: %w", err)
	}
	return out, nil
}

// auditQuery builds the SELECT for f.
func auditQuery(f audit.Filter) (string, []any) {
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
	q := auditSelectSQL
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY timestamp_ns DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	return q, args
}

// scanAuditEntry reads one audit_log row.
func scanAuditEntry(rows *sql.Rows) (audit.Entry, error) {
	var (
		e           audit.Entry
		ts          int64
		actionStr   string
		target      sql.NullString
		detailsJSON sql.NullString
	)
	if err := rows.Scan(&e.ID, &ts, &actionStr, &e.Actor, &target, &detailsJSON); err != nil {
		return audit.Entry{}, fmt.Errorf("audit: scan: %w", err)
	}
	e.Timestamp = time.Unix(0, ts).UTC()
	e.Action = audit.Action(actionStr)
	if target.Valid {
		e.Target = target.String
	}
	if detailsJSON.Valid {
		var d map[string]any
		if err := json.Unmarshal([]byte(detailsJSON.String), &d); err != nil {
			return audit.Entry{}, fmt.Errorf("audit: decode details: %w", err)
		}
		e.Details = d
	}
	return e, nil
}
