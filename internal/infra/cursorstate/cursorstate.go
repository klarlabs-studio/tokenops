// Package cursorstate reads Cursor's chat store for the agent-DX domain.
//
// Cursor keeps conversations in a VS Code-style SQLite key-value store:
// globalStorage/state.vscdb holds a cursorDiskKV table whose rows are
// keyed bubbleId:<composerId>:<bubbleId>, one per message. This package
// is the SQL; what a row means is agentdx's (agentdx.CursorStore).
package cursorstate

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // read-only driver for Cursor's state.vscdb

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// Store reads state.vscdb files. The zero value is ready to use.
type Store struct{}

var _ agentdx.CursorStore = Store{}

// Bubbles calls yield with the key and value of every bubbleId:* row of
// the store at path. The store is opened mode=ro so a running Cursor is
// never disturbed and its committed WAL data is still visible. A store
// without the cursorDiskKV table, or one whose rows cannot be read, is
// reported as agentdx.ErrCursorSchema.
func (Store) Bubbles(path string, yield func(key, value string)) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("agentdx: open Cursor store: %w", err)
	}
	defer func() { _ = db.Close() }()

	if !hasTable(db, "cursorDiskKV") {
		return fmt.Errorf("%w: no cursorDiskKV table in %s", agentdx.ErrCursorSchema, path)
	}
	rows, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE 'bubbleId:%'`)
	if err != nil {
		return fmt.Errorf("%w: %v", agentdx.ErrCursorSchema, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		yield(key, value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: %v", agentdx.ErrCursorSchema, err)
	}
	return nil
}

// hasTable reports whether the database defines the named table.
func hasTable(db *sql.DB, name string) bool {
	var found string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	return err == nil && found == name
}
