// Package sqlite provides a local-first event store backed by SQLite. It is
// the canonical sink for envelopes emitted by the proxy and downstream
// pipelines (optimization, coaching). The schema lives in schema.go and is
// applied incrementally by Open.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"

	msqlite "modernc.org/sqlite" // pure-Go driver registered as "sqlite"
)

// ErrContended marks an append that failed because the store was busy, not
// broken: another connection held the write lock past the busy timeout, or
// the caller's deadline ran out first. Every tokenops process on a machine
// shares one store, so this is routine; the same batch succeeds once the
// other writer finishes.
var ErrContended = errors.New("sqlite: store contended")

// IsContended reports whether err marks a contended store. Callers retry
// such a failure until it clears; anything else will fail identically on
// every attempt.
func IsContended(err error) bool { return errors.Is(err, ErrContended) }

// SQLite primary result codes that mean "busy", not "broken". Extended
// codes carry the primary code in the low byte.
const (
	codeBusy      = 5
	codeLocked    = 6
	codeInterrupt = 9
)

// contended wraps err with ErrContended when it is lock contention or an
// exhausted caller deadline; other errors pass through unchanged.
func contended(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var se *msqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case codeBusy, codeLocked, codeInterrupt:
			return fmt.Errorf("%w: %w", ErrContended, err)
		}
	}
	if ctx.Err() != nil {
		// database/sql reports an interrupted statement in several shapes
		// ("context deadline exceeded", "sql: statement is closed"); the
		// expired context is the reliable signal.
		return fmt.Errorf("%w: %w", ErrContended, err)
	}
	return err
}

// ownerOnly is the mode for the store and its WAL sidecars. They hold usage
// history for every agent session on the machine.
const ownerOnly = 0o600

// restrictToOwner narrows the store and any WAL sidecars to ownerOnly. A
// store created by an older release carries the umask default, so this runs
// on every open and repairs it.
func restrictToOwner(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, ownerOnly); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sqlite: restrict %s: %w", filepath.Base(p), err)
		}
	}
	return nil
}

// Store is a handle to the SQLite-backed event store. It is safe for
// concurrent use; the underlying database/sql connection pool serialises
// writes when SQLite locks for write.
type Store struct {
	db       *sql.DB
	path     string
	logger   *slog.Logger
	slowLock time.Duration
	// skippedInvalid counts envelopes AppendBatch refused to store.
	skippedInvalid atomic.Int64
}

// Options tunes the connection. Zero values fall back to sensible defaults.
type Options struct {
	// MaxOpenConns caps concurrent SQLite connections. SQLite serialises
	// writes, so a small ceiling avoids unnecessary contention. Default 4.
	MaxOpenConns int
	// BusyTimeout configures SQLite's busy timeout pragma. Default 5s.
	BusyTimeout time.Duration
	// Logger receives slow-lock diagnostics. Nil uses slog.Default, so
	// each process reports wherever its own logs already go.
	Logger *slog.Logger
	// SlowLockWarn is how long a write may wait for, or hold, the lock
	// before it is logged. Zero uses defaultSlowLockWarn.
	SlowLockWarn time.Duration
}

// defaultSlowLockWarn flags lock waits and holds far outside the normal
// range: an uncontended 64-row batch commits in about 15ms.
const defaultSlowLockWarn = time.Second

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
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SlowLockWarn <= 0 {
		opts.SlowLockWarn = defaultSlowLockWarn
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

	s := &Store{db: db, path: path, logger: opts.Logger, slowLock: opts.SlowLockWarn}
	started := time.Now()
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if took := time.Since(started); took >= s.slowLock {
		// migrate starts with a schema write, so a slow one waited on
		// another process's write lock.
		s.logSlowLock("open", took, 0, 0)
	}
	if path != ":memory:" {
		if err := restrictToOwner(path); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return s, nil
}

// OpenReadOnly opens an existing store for reads only: no migration, no
// permission repair, and SQLite refuses any write. Short-lived callers on a
// hot path, such as the per-turn hooks, use it so reading a value never
// takes the write lock every tokenops process shares.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: resolve path %q: %w", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("sqlite: open read-only: %w", err)
	}
	v := url.Values{}
	v.Add("mode", "ro")
	v.Add("_pragma", "busy_timeout(2000)")
	v.Add("_pragma", "query_only(1)")
	db, err := sql.Open("sqlite", "file:"+abs+"?"+v.Encode())
	if err != nil {
		return nil, fmt.Errorf("sqlite: open read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping read-only: %w", err)
	}
	return &Store{db: db, path: abs, logger: slog.Default(), slowLock: defaultSlowLockWarn}, nil
}

// LatestAttributesBySource returns the attributes of the newest event from
// source, no older than since, that carries key. ok is false when there is
// none: an older reading is not reported as current.
func (s *Store) LatestAttributesBySource(ctx context.Context, source, key string, since time.Time) (map[string]string, time.Time, bool, error) {
	var (
		raw sql.NullString
		ts  int64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT attributes, timestamp_ns FROM events
WHERE source = ? AND timestamp_ns >= ? AND json_extract(attributes, ?) IS NOT NULL
ORDER BY timestamp_ns DESC LIMIT 1`, source, since.UnixNano(), "$."+key).Scan(&raw, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, fmt.Errorf("sqlite: latest %s attributes: %w", source, err)
	}
	attrs := map[string]string{}
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &attrs); err != nil {
			return nil, time.Time{}, false, fmt.Errorf("sqlite: decode %s attributes: %w", source, err)
		}
	}
	return attrs, time.Unix(0, ts), true, nil
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

// Append persists a single envelope. Unlike AppendBatch it reports an
// invalid envelope as an error: a lone caller can act on it.
func (s *Store) Append(ctx context.Context, env *eventschema.Envelope) error {
	r, err := envelopeToRow(env)
	if err != nil {
		return fmt.Errorf("sqlite: envelope: %w", err)
	}
	return s.insertRows(ctx, []row{r})
}

// AppendBatch persists the valid envelopes of envs atomically: either all
// of them are committed or none are. Empty input is a no-op. An envelope
// that cannot be stored (nil, missing id or type, payload mismatch) is
// skipped, logged and counted in SkippedInvalid rather than failing the
// batch: the bus retries a failed batch and then drops it, so one bad
// envelope would otherwise cost every good row beside it. ON CONFLICT (id)
// the existing row is preserved — emitters are expected to use UUIDv7 /
// unique IDs, so a collision usually means a retried emit and we want it
// to be idempotent. A failure caused by another writer or an exhausted
// deadline satisfies IsContended.
func (s *Store) AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error {
	rows := make([]row, 0, len(envs))
	for i, env := range envs {
		r, err := envelopeToRow(env)
		if err != nil {
			s.skippedInvalid.Add(1)
			id, typ := "", ""
			if env != nil {
				id, typ = env.ID, string(env.Type)
			}
			s.logger.Warn("sqlite: invalid envelope skipped",
				"index", i, "id", id, "type", typ, "err", err)
			continue
		}
		rows = append(rows, r)
	}
	return s.insertRows(ctx, rows)
}

// SkippedInvalid reports how many envelopes AppendBatch has refused to
// store since Open because they were malformed.
func (s *Store) SkippedInvalid() int64 { return s.skippedInvalid.Load() }

// insertRows writes rows in one transaction.
func (s *Store) insertRows(ctx context.Context, rows []row) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", contended(ctx, err))
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("sqlite: prepare: %w", contended(ctx, err))
	}
	defer func() { _ = stmt.Close() }()

	// A deferred transaction takes the write lock at its first insert, so
	// the wait ends, and the hold begins, when that insert returns.
	began := time.Now()
	var locked time.Time
	for i, r := range rows {
		if i == 1 {
			locked = time.Now()
		}
		if _, err := stmt.ExecContext(ctx,
			r.ID, r.SchemaVersion, string(r.Type), r.TimestampNS, r.Day,
			r.TraceID, r.SpanID, r.Source,
			r.Provider, r.Model,
			r.WorkflowID, r.AgentID, r.SessionID, r.UserID,
			r.WorkID, r.ExecutionID, r.ActorID,
			r.DecisionID, r.InterventionID, r.ExperimentID,
			r.InputTokens, r.OutputTokens, r.TotalTokens, r.CostUSD,
			r.Payload, r.Attributes,
		); err != nil {
			return fmt.Errorf("sqlite: insert row %d (%s): %w", i, r.ID, contended(ctx, err))
		}
	}
	if locked.IsZero() {
		locked = time.Now()
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", contended(ctx, err))
	}
	wait, held := locked.Sub(began), time.Since(locked)
	if wait >= s.slowLock || held >= s.slowLock {
		s.logSlowLock("append", wait, held, len(rows))
	}
	return nil
}

// logSlowLock names the process that saw a slow write-lock wait or hold.
// Every tokenops process logs its own, so the holder of a contended lock
// appears as the one reporting a long hold while others report waits.
func (s *Store) logSlowLock(op string, wait, held time.Duration, rows int) {
	role := ""
	if len(os.Args) > 1 {
		role = os.Args[1]
	}
	s.logger.Warn("sqlite: slow write lock",
		"op", op, "wait", wait.Round(time.Millisecond), "held", held.Round(time.Millisecond),
		"rows", rows, "pid", os.Getpid(), "role", role)
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
	// Work, Execution and Actor filter on the ontology association.
	// Filtering by execution is what makes two attempts at the same goal
	// comparable, which every experiment needs.
	Work         string
	Execution    string
	Actor        string
	Decision     string
	Intervention string
	Experiment   string
	Since        time.Time
	Until        time.Time
	Limit        int
}

const defaultQueryLimit = 1000

// Query returns envelopes matching the filter, ordered by timestamp (then
// id) ascending. When more rows match than the limit, the newest rows are
// kept: the limit trims the oldest end of the window.
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
	if f.Work != "" {
		conds = append(conds, "work_id = ?")
		args = append(args, f.Work)
	}
	if f.Execution != "" {
		conds = append(conds, "execution_id = ?")
		args = append(args, f.Execution)
	}
	if f.Actor != "" {
		conds = append(conds, "actor_id = ?")
		args = append(args, f.Actor)
	}
	if f.Decision != "" {
		conds = append(conds, "decision_id = ?")
		args = append(args, f.Decision)
	}
	if f.Intervention != "" {
		conds = append(conds, "intervention_id = ?")
		args = append(args, f.Intervention)
	}
	if f.Experiment != "" {
		conds = append(conds, "experiment_id = ?")
		args = append(args, f.Experiment)
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

	// The limit keeps the newest rows in the window — a capped read is a
	// "latest N" view, and the oldest rows are the ones that matter least —
	// and the outer query returns them oldest first, as callers iterate.
	inner := selectSQL
	if len(conds) > 0 {
		inner += " WHERE " + strings.Join(conds, " AND ")
	}
	inner += " ORDER BY timestamp_ns DESC, id DESC LIMIT ?"
	q := "SELECT * FROM (" + inner + ") ORDER BY timestamp_ns ASC, id ASC"
	args = append(args, limit)

	rs, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query: %w", err)
	}
	defer func() { _ = rs.Close() }()
	return scanEnvelopes(rs)
}

// readPage is how many rows ReadEvents fetches per query.
const readPage = 5000

// ReadEvents returns every event of type t at or after since, oldest
// first. Unlike Query it has no cap: it reads in pages keyed on
// (timestamp, id). A capped read dropped the newest rows of a busy month,
// which are the ones a "latest reading" or a month's spend most needs.
func (s *Store) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return readEvents(ctx, s.db, t, since)
}

// querier is what a read runs on: the pool, or one transaction when
// several reads must see the same snapshot.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readEvents is ReadEvents on q.
func readEvents(ctx context.Context, q querier, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	err := readEventPages(ctx, q, selectSQL, t, since, func(rs *sql.Rows) (*eventschema.Envelope, error) {
		env, err := scanEnvelope(rs)
		if err == nil {
			out = append(out, env)
		}
		return env, err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readEventPages runs ReadEvents' paged read of sel, a selectSQL with
// any leading columns of the caller's, handing each row to scan, which
// returns the envelope the row decoded to.
func readEventPages(ctx context.Context, q querier, sel string, t eventschema.EventType, since time.Time,
	scan func(*sql.Rows) (*eventschema.Envelope, error)) error {
	var (
		lastTS = since.UTC().UnixNano() - 1
		lastID = ""
	)
	// The plain range bound lets SQLite seek on (type, timestamp_ns); the
	// OR alone made every page a scan.
	for {
		rs, err := q.QueryContext(ctx, sel+`
WHERE type = ? AND timestamp_ns >= ? AND (timestamp_ns > ? OR id > ?)
ORDER BY timestamp_ns ASC, id ASC LIMIT ?`, string(t), lastTS, lastTS, lastID, readPage)
		if err != nil {
			return fmt.Errorf("sqlite: read events: %w", err)
		}
		var (
			n    int
			last *eventschema.Envelope
		)
		for rs.Next() {
			if last, err = scan(rs); err != nil {
				_ = rs.Close()
				return err
			}
			n++
		}
		err = rs.Err()
		_ = rs.Close()
		if err != nil {
			return fmt.Errorf("sqlite: rows: %w", err)
		}
		if n < readPage {
			return nil
		}
		lastTS, lastID = last.Timestamp.UTC().UnixNano(), last.ID
	}
}

// scanEnvelopes decodes every row of rs.
func scanEnvelopes(rs *sql.Rows) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for rs.Next() {
		env, err := scanEnvelope(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: rows: %w", err)
	}
	return out, nil
}

// scanEnvelope decodes rs's current row, a selectSQL row after any
// leading columns, which are scanned into lead.
func scanEnvelope(rs *sql.Rows, lead ...any) (*eventschema.Envelope, error) {
	var (
		r       row
		typeStr string
	)
	if err := rs.Scan(append(lead,
		&r.ID, &r.SchemaVersion, &typeStr, &r.TimestampNS, &r.Day,
		&r.TraceID, &r.SpanID, &r.Source,
		&r.Provider, &r.Model,
		&r.WorkflowID, &r.AgentID, &r.SessionID, &r.UserID,
		&r.WorkID, &r.ExecutionID, &r.ActorID,
		&r.DecisionID, &r.InterventionID, &r.ExperimentID,
		&r.InputTokens, &r.OutputTokens, &r.TotalTokens, &r.CostUSD,
		&r.Payload, &r.Attributes,
	)...); err != nil {
		return nil, fmt.Errorf("sqlite: scan: %w", err)
	}
	r.Type = eventschema.EventType(typeStr)
	env, err := rowToEnvelope(r)
	if err != nil {
		return nil, fmt.Errorf("sqlite: decode %s: %w", r.ID, err)
	}
	return env, nil
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
	if f.Work != "" {
		conds = append(conds, "work_id = ?")
		args = append(args, f.Work)
	}
	if f.Execution != "" {
		conds = append(conds, "execution_id = ?")
		args = append(args, f.Execution)
	}
	if f.Actor != "" {
		conds = append(conds, "actor_id = ?")
		args = append(args, f.Actor)
	}
	if f.Decision != "" {
		conds = append(conds, "decision_id = ?")
		args = append(args, f.Decision)
	}
	if f.Intervention != "" {
		conds = append(conds, "intervention_id = ?")
		args = append(args, f.Intervention)
	}
	if f.Experiment != "" {
		conds = append(conds, "experiment_id = ?")
		args = append(args, f.Experiment)
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

// ExperimentEvents implements the bounded-experiment ledger port without
// exposing sqlite query types to the capability layer.
func (s *Store) ExperimentEvents(ctx context.Context, id string) ([]*eventschema.Envelope, error) {
	filter := Filter{Experiment: id, Limit: 100_000}
	if id == "" {
		// Listing active experiments needs lifecycle events only. Reading one
		// experiment returns every correlated event so learning can join the
		// decision, prompt and outcome evidence without a second data source.
		filter.Type = eventschema.EventTypeExperiment
	}
	return s.Query(ctx, filter)
}

const insertSQL = `
INSERT INTO events (
    id, schema_version, type, timestamp_ns, day,
    trace_id, span_id, source,
    provider, model,
    workflow_id, agent_id, session_id, user_id,
    work_id, execution_id, actor_id,
    decision_id, intervention_id, experiment_id,
    input_tokens, output_tokens, total_tokens, cost_usd,
    payload, attributes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING
`

const selectSQL = `
SELECT
    id, schema_version, type, timestamp_ns, day,
    trace_id, span_id, source,
    provider, model,
    workflow_id, agent_id, session_id, user_id,
    work_id, execution_id, actor_id,
    decision_id, intervention_id, experiment_id,
    input_tokens, output_tokens, total_tokens, cost_usd,
    payload, attributes
FROM events
`

// CountBySource returns event counts grouped by the source column,
// optionally constrained to a since/until window. NULL sources are
// returned under the "(none)" bucket so dashboards can distinguish
// "real proxy traffic missing its label" from "MCP-session ping".
// Used by tokenops_status (view=data_sources) and `tokenops_status.data_sources`
// so operators can inspect per-source ingestion coverage at a glance.
func (s *Store) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite: store not initialised")
	}
	var (
		conds []string
		args  []any
	)
	if !since.IsZero() {
		conds = append(conds, "timestamp_ns >= ?")
		args = append(args, since.UTC().UnixNano())
	}
	if !until.IsZero() {
		conds = append(conds, "timestamp_ns < ?")
		args = append(args, until.UTC().UnixNano())
	}
	q := "SELECT COALESCE(NULLIF(source, ''), '(none)') AS src, COUNT(*) FROM events"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " GROUP BY src ORDER BY 2 DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count-by-source: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var src string
		var n int64
		if err := rows.Scan(&src, &n); err != nil {
			return nil, err
		}
		out[src] = n
	}
	return out, rows.Err()
}

// buildDSN assembles a modernc.org/sqlite DSN with sane defaults: WAL
// journaling, NORMAL synchronous, 5-second busy timeout, foreign keys on,
// incremental auto-vacuum. The last takes effect when a store is created,
// and on an existing store at its next VACUUM: retention can then return
// freed pages in short chunks rather than rewrite the whole database under
// the write lock.
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
	v.Add("_pragma", "auto_vacuum(INCREMENTAL)")
	return "file:" + raw + "?" + v.Encode(), nil
}

// EachEventID streams the ID of every stored envelope to fn, in no
// particular order. It reads the primary-key index only, so it is cheap
// relative to Query even on a large store.
func (s *Store) EachEventID(ctx context.Context, fn func(id string)) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM events`)
	if err != nil {
		return fmt.Errorf("sqlite: list event ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("sqlite: scan event id: %w", err)
		}
		fn(id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: iterate event ids: %w", err)
	}
	return nil
}

// LastEventBySource returns the most recent event timestamp per source.
//
// CountBySource answers "were there events in a window", which is enough to
// notice ingestion has stopped but not to say for how long. A warning built on
// it alone reads identically on the second day of an outage and the
// twenty-seventh, so nothing conveys that a gap is growing. This supplies the
// missing half: the age of the newest event a source produced.
//
// A source with no events at all is absent from the result rather than present
// with a zero time, so callers can distinguish "never ingested" from "ingested
// at the epoch".
func (s *Store) LastEventBySource(ctx context.Context) (map[string]time.Time, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite: store not initialised")
	}
	const q = `SELECT COALESCE(NULLIF(source, ''), '(none)') AS src, MAX(timestamp_ns)
	           FROM events GROUP BY src`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: last-event-by-source: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]time.Time{}
	for rows.Next() {
		var (
			src string
			ns  sql.NullInt64
		)
		if err := rows.Scan(&src, &ns); err != nil {
			return nil, fmt.Errorf("sqlite: last-event-by-source scan: %w", err)
		}
		if !ns.Valid {
			continue
		}
		out[src] = time.Unix(0, ns.Int64).UTC()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: last-event-by-source rows: %w", err)
	}
	return out, nil
}

// RestampPlanIncluded marks a provider's metered prompt events in
// [from, to) as covered by a plan, and returns how many it changed.
//
// Cost is otherwise fixed when an event is recorded, from the plan bound
// at that moment. This is the one deliberate exception (ADR 0008): an
// operator who tells TokenOps they were on a plan all along corrects the
// usage it recorded as billed while no plan was bound. Events already
// plan-covered or on a trial are left alone.
func (s *Store) RestampPlanIncluded(ctx context.Context, provider string, from, to time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE events
		SET payload = json_remove(json_set(payload, '$.cost_source', 'plan_included'), '$.cost_usd', '$.cost_measured'),
		    cost_usd = NULL
		WHERE type = 'prompt' AND provider = ? AND timestamp_ns >= ? AND timestamp_ns < ?
		  AND coalesce(json_extract(payload, '$.cost_source'), '') IN ('', 'metered')`,
		provider, from.UTC().UnixNano(), to.UTC().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("sqlite: restamp: %w", contended(ctx, err))
	}
	return res.RowsAffected()
}

// RestampMetered marks a provider's plan-covered prompt events in
// [from, to) as billed per token, and returns how many it changed. Their
// cost is cleared, so it is priced when read, like any billed request.
//
// It is the reverse of RestampPlanIncluded, for a spend-denominated plan
// (usage-based Enterprise, ADR 0009): such a plan covers nothing, and
// usage stamped as covered while it was bound reported real spend as $0.
// Events on a trial are left alone.
func (s *Store) RestampMetered(ctx context.Context, provider string, from, to time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE events
		SET payload = json_remove(json_set(payload, '$.cost_source', 'metered'), '$.cost_usd', '$.cost_measured'),
		    cost_usd = NULL
		WHERE type = 'prompt' AND provider = ? AND timestamp_ns >= ? AND timestamp_ns < ?
		  AND json_extract(payload, '$.cost_source') = 'plan_included'`,
		provider, from.UTC().UnixNano(), to.UTC().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("sqlite: restamp metered: %w", contended(ctx, err))
	}
	return res.RowsAffected()
}

// ModelsFor lists the distinct models of a source's prompt events recorded
// under provider.
func (s *Store) ModelsFor(ctx context.Context, source, provider string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT model FROM events
		WHERE type = 'prompt' AND source = ? AND provider = ? AND model IS NOT NULL AND model != ''`, source, provider)
	if err != nil {
		return nil, fmt.Errorf("sqlite: models for: %w", contended(ctx, err))
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("sqlite: models for: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Reattribute moves a source's prompt events for model, recorded in
// [since, until), from one provider to another and records the endpoint
// they went through; it returns how many it changed (ADR 0009). When
// uncover is set, events marked as covered by the old provider's plan
// become billed, with their cost cleared so it is priced on read: a plan
// covers only its own provider's usage.
func (s *Store) Reattribute(ctx context.Context, source, model, from, to, endpoint string, since, until time.Time, uncover bool) (int64, error) {
	payload := `json_set(payload, '$.provider', ?)`
	cost := `cost_usd`
	args := make([]any, 0, 9)
	if uncover {
		payload = `CASE WHEN json_extract(payload, '$.cost_source') = 'plan_included'
			THEN json_remove(json_set(payload, '$.provider', ?, '$.cost_source', 'metered'), '$.cost_usd', '$.cost_measured')
			ELSE json_set(payload, '$.provider', ?) END`
		cost = `CASE WHEN json_extract(payload, '$.cost_source') = 'plan_included' THEN NULL ELSE cost_usd END`
		args = append(args, to)
	}
	args = append(args, to, endpoint, to, source, from, model, since.UTC().UnixNano(), until.UTC().UnixNano())
	res, err := s.db.ExecContext(ctx, `UPDATE events SET cost_usd = `+cost+`, payload = `+payload+`,
		attributes = json_set(coalesce(attributes, '{}'), '$.endpoint', ?), provider = ?
		WHERE type = 'prompt' AND source = ? AND provider = ? AND model = ?
		  AND timestamp_ns >= ? AND timestamp_ns < ?`, args...)
	if err != nil {
		return 0, fmt.Errorf("sqlite: reattribute: %w", contended(ctx, err))
	}
	return res.RowsAffected()
}

// SessionModels lists the distinct models of one session's prompt events
// recorded under provider, sorted; a turn with no model is listed as "".
func (s *Store) SessionModels(ctx context.Context, source, provider, session string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT coalesce(model, '') AS m FROM events
		WHERE type = 'prompt' AND source = ? AND provider = ? AND session_id = ? ORDER BY m`, source, provider, session)
	if err != nil {
		return nil, fmt.Errorf("sqlite: session models: %w", contended(ctx, err))
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("sqlite: session models: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReattributeSession moves a source's prompt events for one session and
// model ("" for turns with none) from one provider to another and records
// their endpoint; it returns how many it changed (ADR 0009). With uncover,
// events marked as covered by the old provider's plan become billed.
//
// It is per model because the biller is: a gateway running OpenAI's
// models on the operator's own credential bills its own models and
// leaves OpenAI's with OpenAI, in the same session.
func (s *Store) ReattributeSession(ctx context.Context, source, session, model, from, to, endpoint string, uncover bool) (int64, error) {
	payload, cost := `json_set(payload, '$.provider', ?)`, `cost_usd`
	args := make([]any, 0, 8)
	if uncover {
		payload = `CASE WHEN json_extract(payload, '$.cost_source') = 'plan_included'
			THEN json_remove(json_set(payload, '$.provider', ?, '$.cost_source', 'metered'), '$.cost_usd', '$.cost_measured')
			ELSE json_set(payload, '$.provider', ?) END`
		cost = `CASE WHEN json_extract(payload, '$.cost_source') = 'plan_included' THEN NULL ELSE cost_usd END`
		args = append(args, to)
	}
	// Only rows not already where they are going: from may equal to, when
	// a turn keeps its provider and gains its endpoint.
	args = append(args, to, endpoint, to, source, from, session, model, to, endpoint, uncover)
	res, err := s.db.ExecContext(ctx, `UPDATE events SET cost_usd = `+cost+`, payload = `+payload+`,
		attributes = json_set(coalesce(attributes, '{}'), '$.endpoint', ?), provider = ?
		WHERE type = 'prompt' AND source = ? AND provider = ? AND session_id = ? AND coalesce(model, '') = ?
		  AND (provider != ? OR coalesce(json_extract(attributes, '$.endpoint'), '') != ?
		       OR (? AND json_extract(payload, '$.cost_source') = 'plan_included'))`, args...)
	if err != nil {
		return 0, fmt.Errorf("sqlite: reattribute session: %w", contended(ctx, err))
	}
	return res.RowsAffected()
}

// ProvidersFor lists the distinct providers of a source's prompt events.
func (s *Store) ProvidersFor(ctx context.Context, source string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT provider FROM events
		WHERE type = 'prompt' AND source = ? AND provider IS NOT NULL AND provider != ''`, source)
	if err != nil {
		return nil, fmt.Errorf("sqlite: providers for: %w", contended(ctx, err))
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("sqlite: providers for: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RenameProvider moves all of a source's prompt events from one provider
// name to another and records their endpoint; it returns how many it
// changed. It corrects a provider recorded under a raw client ID
// (opencode's "zai-coding-plan") that now maps to TokenOps' name.
func (s *Store) RenameProvider(ctx context.Context, source, from, to, endpoint string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE events SET payload = json_set(payload, '$.provider', ?),
		attributes = json_set(coalesce(attributes, '{}'), '$.endpoint', ?), provider = ?
		WHERE type = 'prompt' AND source = ? AND provider = ?`, to, endpoint, to, source, from)
	if err != nil {
		return 0, fmt.Errorf("sqlite: rename provider: %w", contended(ctx, err))
	}
	return res.RowsAffected()
}

// MarkEndpoint records the endpoint a source's prompt events for provider
// went through in [since, until), for events that carry none, and returns
// how many it changed. Through an endpoint other than the provider's own,
// a turn is not covered by the provider's plan (ADR 0009), so one marked
// as covered becomes billed, with its cost cleared to be priced on read.
func (s *Store) MarkEndpoint(ctx context.Context, source, provider, endpoint string, since, until time.Time) (int64, error) {
	uncover := endpoint != provider
	payload, cost := `payload`, `cost_usd`
	if uncover {
		payload = `CASE WHEN json_extract(payload, '$.cost_source') = 'plan_included'
			THEN json_remove(json_set(payload, '$.cost_source', 'metered'), '$.cost_usd', '$.cost_measured')
			ELSE payload END`
		cost = `CASE WHEN json_extract(payload, '$.cost_source') = 'plan_included' THEN NULL ELSE cost_usd END`
	}
	res, err := s.db.ExecContext(ctx, `UPDATE events SET cost_usd = `+cost+`, payload = `+payload+`,
		attributes = json_set(coalesce(attributes, '{}'), '$.endpoint', ?)
		WHERE type = 'prompt' AND source = ? AND provider = ?
		  AND timestamp_ns >= ? AND timestamp_ns < ?
		  AND json_extract(coalesce(attributes, '{}'), '$.endpoint') IS NULL`,
		endpoint, source, provider, since.UTC().UnixNano(), until.UTC().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("sqlite: mark endpoint: %w", contended(ctx, err))
	}
	return res.RowsAffected()
}
