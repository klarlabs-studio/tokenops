// Package opencodedb is the adapter for opencode's local SQLite store: it
// implements the telemetry/opencodedb Reader port the domains read through,
// and answers the probes the infrastructure needs (the newest message, each
// session's directory).
//
// The SQL still lives in internal/contexts/telemetry/opencodedb, because
// governance/agentdx calls that package's Read directly. Every other reader
// already goes through this package, so once agentdx takes the port too the
// SQL moves here and no caller changes.
package opencodedb

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
)

// Store reads opencode's database. The zero value is ready to use; it holds
// no connection, each call opens the file read-only and closes it.
type Store struct{}

var _ opencodedb.Reader = Store{}

// Read implements opencodedb.Reader.
func (Store) Read(path string, opts opencodedb.Options, visit func(opencodedb.Message) error) error {
	return opencodedb.Read(path, opts, visit)
}

// Newest is the time of the newest message in either shape, and whether
// the store could be read.
func Newest(path string) (time.Time, bool) { return opencodedb.Newest(path) }

// SessionDirs maps each opencode session to the directory it ran in. A
// non-zero since keeps the sessions active since then.
func SessionDirs(path string, since time.Time) (map[string]string, error) {
	return opencodedb.SessionDirs(path, since)
}
