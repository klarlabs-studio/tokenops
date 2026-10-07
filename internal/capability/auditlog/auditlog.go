// Package auditlog reads the audit log: who changed configuration, plans,
// budgets and optimizations, and when. `tokenops audit`, the MCP audit
// view and the daemon's GET /api/audit all answer from here (ADR 0010),
// so no two surfaces can filter the same log differently.
//
// A query written as text (the HTTP form) is parsed in one step and run
// in another, so an adapter can tell a caller's mistake (a window that
// does not parse) from a store failure without inspecting error text.
package auditlog

import (
	"context"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Entry is one audited change. Aliased so the audit domain stays its
// single definition.
type Entry = audit.Entry

// Query narrows the log. Empty fields are not constrained; Limit is
// passed through as given.
type Query struct {
	Action string
	Actor  string
	Since  time.Time
	Until  time.Time
	Limit  int
}

// Log is the answer, newest first.
type Log struct {
	Entries []Entry `json:"entries"`
}

// Read returns the entries q selects from store's audit log.
func Read(ctx context.Context, store *sqlite.Store, q Query) (Log, error) {
	entries, err := audit.NewRecorder(store).Query(ctx, audit.Filter{
		Action: audit.Action(q.Action),
		Actor:  q.Actor,
		Since:  q.Since,
		Until:  q.Until,
		Limit:  q.Limit,
	})
	if err != nil {
		return Log{}, err
	}
	return Log{Entries: entries}, nil
}

// defaultLookback is a written query's window when it names no since.
const defaultLookback = 24 * time.Hour

// defaultLimit caps a written query when it names no positive limit.
const defaultLimit = 100

// Request is a query as a caller wrote it: since is RFC 3339 or a
// duration such as 24h (24h when empty), until is RFC 3339 (open when
// empty), limit is a positive integer (100 otherwise).
type Request struct {
	Since, Until  string
	Action, Actor string
	Limit         string
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
	return Query{Action: r.Action, Actor: r.Actor, Since: f.Since, Until: f.Until, Limit: limit}, nil
}
