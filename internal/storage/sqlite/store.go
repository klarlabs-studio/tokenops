// Package sqlite provides a local-first event store backed by SQLite. It is
// the canonical sink for envelopes emitted by the proxy and downstream
// pipelines (optimization, coaching). The schema lives in schema.go and is
// applied incrementally by Open.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"

	_ "modernc.org/sqlite" // pure-Go driver registered as "sqlite"
)

// Store is a handle to the SQLite-backed event store. It is safe for
// concurrent use; the underlying database/sql connection pool serialises
// writes when SQLite locks for write.
type Store struct {
	db   *sql.DB
	path string
}

// Options tunes the connection. Zero values fall back to sensible defaults.
type Options struct {
	// MaxOpenConns caps concurrent SQLite connections. SQLite serialises
	// writes, so a small ceiling avoids unnecessary contention. Default 4.
	MaxOpenConns int
	// BusyTimeout configures SQLite's busy timeout pragma. Default 5s.
	BusyTimeout time.Duration
}

// Open connects to (or creates) the SQLite database at path and runs any
// pending migrations. The special path ":memory:" is honoured for tests.
func Open(ctx context.Context, path string, opts Options) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: path must not be empty")
	}
	if opts.MaxOpenConns <= 0 {
		opts.MaxOpenConns = 4
	}
	if opts.BusyTimeout <= 0 {
		opts.BusyTimeout = 5 * time.Second
	}

	dsn, err := buildDSN(path, opts.BusyTimeout)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(opts.MaxOpenConns)
	db.SetMaxIdleConns(opts.MaxOpenConns)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB. Reserved for advanced callers (e.g.
// analytics aggregator) that need to run their own queries; ordinary callers
// should prefer Append/Query.
func (s *Store) DB() *sql.DB { return s.db }

// Append persists a single envelope. It is a convenience wrapper around
// AppendBatch and inherits the same transactional guarantees.
func (s *Store) Append(ctx context.Context, env *eventschema.Envelope) error {
	return s.AppendBatch(ctx, []*eventschema.Envelope{env})
}

// AppendBatch persists envs atomically. Either all rows are committed or
// none are. Empty input is a no-op. ON CONFLICT (id) the existing row is
// preserved — emitters are expected to use UUIDv7 / unique IDs, so a
// collision usually means a retried emit and we want it to be idempotent.
func (s *Store) AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error {
	if len(envs) == 0 {
		return nil
	}
	rows := make([]row, len(envs))
	for i, env := range envs {
		r, err := envelopeToRow(env)
		if err != nil {
			return fmt.Errorf("sqlite: envelope %d: %w", i, err)
		}
		rows[i] = r
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("sqlite: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i, r := range rows {
		if _, err := stmt.ExecContext(ctx,
			r.ID, r.SchemaVersion, string(r.Type), r.TimestampNS, r.Day,
			r.TraceID, r.SpanID, r.Source,
			r.Provider, r.Model,
			r.WorkflowID, r.AgentID, r.SessionID, r.UserID,
			r.InputTokens, r.OutputTokens, r.TotalTokens, r.CostUSD,
			r.Payload, r.Attributes,
		); err != nil {
			return fmt.Errorf("sqlite: insert row %d (%s): %w", i, r.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}

// Filter selects events for Query. Empty fields are not constrained; Limit <=
// 0 is rewritten to a default ceiling so unbounded scans are never trivially
// triggered.
type Filter struct {
	Type       eventschema.EventType
	WorkflowID string
	AgentID    string
	SessionID  string
	Provider   string
	Model      string
	Since      time.Time
	Until      time.Time
	Limit      int
}

const defaultQueryLimit = 1000

// Query returns envelopes matching the filter, ordered by timestamp ascending.
func (s *Store) Query(ctx context.Context, f Filter) ([]*eventschema.Envelope, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}

	var (
		conds []string
		args  []any
	)
	if f.Type != "" {
		conds = append(conds, "type = ?")
		args = append(args, string(f.Type))
	}
	if f.WorkflowID != "" {
		conds = append(conds, "workflow_id = ?")
		args = append(args, f.WorkflowID)
	}
	if f.AgentID != "" {
		conds = append(conds, "agent_id = ?")
		args = append(args, f.AgentID)
	}
	if f.SessionID != "" {
		conds = append(conds, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.Provider != "" {
		conds = append(conds, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		conds = append(conds, "model = ?")
		args = append(args, f.Model)
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
	q += " ORDER BY timestamp_ns ASC LIMIT ?"
	args = append(args, limit)

	rs, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query: %w", err)
	}
	defer func() { _ = rs.Close() }()

	var out []*eventschema.Envelope
	for rs.Next() {
		var (
			r       row
			typeStr string
		)
		if err := rs.Scan(
			&r.ID, &r.SchemaVersion, &typeStr, &r.TimestampNS, &r.Day,
			&r.TraceID, &r.SpanID, &r.Source,
			&r.Provider, &r.Model,
			&r.WorkflowID, &r.AgentID, &r.SessionID, &r.UserID,
			&r.InputTokens, &r.OutputTokens, &r.TotalTokens, &r.CostUSD,
			&r.Payload, &r.Attributes,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan: %w", err)
		}
		r.Type = eventschema.EventType(typeStr)
		env, err := rowToEnvelope(r)
		if err != nil {
			return nil, fmt.Errorf("sqlite: decode %s: %w", r.ID, err)
		}
		out = append(out, env)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: rows: %w", err)
	}
	return out, nil
}

// Count returns the number of rows matching the filter. It uses the same
// predicates as Query but ignores Limit.
func (s *Store) Count(ctx context.Context, f Filter) (int64, error) {
	var (
		conds []string
		args  []any
	)
	if f.Type != "" {
		conds = append(conds, "type = ?")
		args = append(args, string(f.Type))
	}
	if f.WorkflowID != "" {
		conds = append(conds, "workflow_id = ?")
		args = append(args, f.WorkflowID)
	}
	if f.AgentID != "" {
		conds = append(conds, "agent_id = ?")
		args = append(args, f.AgentID)
	}
	if f.SessionID != "" {
		conds = append(conds, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.Provider != "" {
		conds = append(conds, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		conds = append(conds, "model = ?")
		args = append(args, f.Model)
	}
	if !f.Since.IsZero() {
		conds = append(conds, "timestamp_ns >= ?")
		args = append(args, f.Since.UTC().UnixNano())
	}
	if !f.Until.IsZero() {
		conds = append(conds, "timestamp_ns < ?")
		args = append(args, f.Until.UTC().UnixNano())
	}
	q := "SELECT COUNT(*) FROM events"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	var n int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: count: %w", err)
	}
	return n, nil
}

const insertSQL = `
INSERT INTO events (
    id, schema_version, type, timestamp_ns, day,
    trace_id, span_id, source,
    provider, model,
    workflow_id, agent_id, session_id, user_id,
    input_tokens, output_tokens, total_tokens, cost_usd,
    payload, attributes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING
`

const selectSQL = `
SELECT
    id, schema_version, type, timestamp_ns, day,
    trace_id, span_id, source,
    provider, model,
    workflow_id, agent_id, session_id, user_id,
    input_tokens, output_tokens, total_tokens, cost_usd,
    payload, attributes
FROM events
`

// buildDSN assembles a modernc.org/sqlite DSN with sane defaults: WAL
// journaling, NORMAL synchronous, 5-second busy timeout, foreign keys on.
// The ":memory:" sentinel is special-cased (no path resolution).
func buildDSN(path string, busy time.Duration) (string, error) {
	var raw string
	if path == ":memory:" {
		raw = ":memory:"
	} else {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("sqlite: resolve path %q: %w", path, err)
		}
		raw = abs
	}
	v := url.Values{}
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "synchronous(NORMAL)")
	v.Add("_pragma", "foreign_keys(ON)")
	v.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()))
	return "file:" + raw + "?" + v.Encode(), nil
}
