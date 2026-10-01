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

// Append persists a single envelope. It is a convenience wrapper around
// AppendBatch and inherits the same transactional guarantees.
func (s *Store) Append(ctx context.Context, env *eventschema.Envelope) error {
	return s.AppendBatch(ctx, []*eventschema.Envelope{env})
}

// AppendBatch persists envs atomically. Either all rows are committed or
// none are. Empty input is a no-op. ON CONFLICT (id) the existing row is
// preserved — emitters are expected to use UUIDv7 / unique IDs, so a
// collision usually means a retried emit and we want it to be idempotent.
// A failure caused by another writer or an exhausted deadline satisfies
// IsContended.
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
			&r.WorkID, &r.ExecutionID, &r.ActorID,
			&r.DecisionID, &r.InterventionID, &r.ExperimentID,
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
// Used by tokenops_data_sources and `tokenops_status.data_sources`
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
