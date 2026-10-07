// Package auditlog reads the audit log: who changed configuration, plans,
// budgets and optimizations, and when. `tokenops audit` and the MCP
// audit view both answer from here (ADR 0010), so the CLI and an agent
// cannot filter the same log differently.
package auditlog

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Entry is one audited change.
type Entry = audit.Entry

// Query narrows the log. Empty fields are not constrained; Limit is
// passed through, so each surface keeps its own default.
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
