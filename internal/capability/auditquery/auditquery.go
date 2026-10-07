// Package auditquery answers "what did TokenOps and its operators do":
// the audit log, filtered by action, actor and window. The daemon's
// GET /api/audit serves it (ADR 0010).
//
// The query is parsed in one step and run in another so an adapter can
// tell a caller's mistake (a window that does not parse) from a store
// failure without inspecting error text.
package auditquery

import (
	"context"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Entry is one audited action. Aliased so the audit domain stays its
// single definition.
type Entry = audit.Entry

// defaultLookback is the window when the caller names no since.
const defaultLookback = 24 * time.Hour

// defaultLimit caps the entries when the caller names no positive limit.
const defaultLimit = 100

// Request is the query as a caller wrote it: since is RFC 3339 or a
// duration such as 24h (24h when empty), until is RFC 3339 (open when
// empty), limit is a positive integer (100 otherwise).
type Request struct {
	Since, Until  string
	Action, Actor string
	Limit         string
}

// Query is a parsed request, ready to run.
type Query struct {
	filter audit.Filter
}

// Parse validates the window. Its error is the caller's mistake and its
// text is fit to show them; an unparsable limit falls back to the
// default rather than failing.
func Parse(r Request) (Query, error) {
	f, err := analytics.QueryParams{
		Since:        r.Since,
		Until:        r.Until,
		DefaultSince: defaultLookback,
	}.ToFilter()
	if err != nil {
		return Query{}, err
	}
	limit, _ := strconv.Atoi(r.Limit)
	if limit <= 0 {
		limit = defaultLimit
	}
	return Query{filter: audit.Filter{
		Action: audit.Action(r.Action),
		Actor:  r.Actor,
		Since:  f.Since,
		Until:  f.Until,
		Limit:  limit,
	}}, nil
}

// Log is the audit log the queries read.
type Log struct {
	rec *audit.Recorder
}

// NewLog reads the audit log in store. A nil store has no log.
func NewLog(store *sqlite.Store) *Log {
	if store == nil {
		return nil
	}
	return &Log{rec: audit.NewRecorder(store)}
}

// Entries is the answer: matching entries, newest first.
type Entries struct {
	Entries []Entry `json:"entries"`
}

// List runs the query.
func (l *Log) List(ctx context.Context, q Query) (Entries, error) {
	entries, err := l.rec.Query(ctx, q.filter)
	if err != nil {
		return Entries{}, err
	}
	return Entries{Entries: entries}, nil
}
