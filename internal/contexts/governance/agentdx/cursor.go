package agentdx

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite" // read-only driver for Cursor's state.vscdb
)

// ErrCursorSchema reports that a Cursor database was found but did not
// contain the tables this reader knows how to read.
//
// It is a distinct error rather than an empty result on purpose. Cursor's
// storage is undocumented and has changed shape before; if a future
// version moves the keys, an operator must be told the reader broke
// rather than shown a report claiming they did no work. Every metric that
// was silently wrong before this — a window meter reading 0/200 under
// full load, compactions reading zero on a corpus full of them — failed
// exactly that way: a read failure rendered as an absence of activity.
var ErrCursorSchema = errors.New("agentdx: unrecognised Cursor database schema")

// CursorDefaultRoot returns Cursor's user-data directory.
func CursorDefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User"), nil
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Cursor", "User"), nil
	default:
		return filepath.Join(home, ".config", "Cursor", "User"), nil
	}
}

// bubbleTypes are Cursor's message roles. 1 is what the operator typed,
// 2 is the model's reply.
const (
	cursorBubbleUser      = 1
	cursorBubbleAssistant = 2
)

// cursorBubble is one message row from cursorDiskKV.
type cursorBubble struct {
	Type       int    `json:"type"`
	Text       string `json:"text"`
	CreatedAt  int64  `json:"createdAt"`
	TokenCount struct {
		InputTokens int64 `json:"inputTokens"`
	} `json:"tokenCount"`
	ToolFormerData *struct {
		Name string `json:"name"`
	} `json:"toolFormerData,omitempty"`
}

// ExtractCursor reads Cursor's chat store into Records.
//
// Cursor keeps conversations in a VS Code-style SQLite key-value store:
// globalStorage/state.vscdb holds a cursorDiskKV table whose rows are
// keyed bubbleId:<composerId>:<bubbleId>, one per message. The composer
// id is the session.
//
// A missing store is not an error — most operators do not run Cursor, and
// reporting a failure for that would make the common case look broken.
// A store that exists but cannot be understood IS an error; see
// ErrCursorSchema.
func ExtractCursor(opts ExtractOptions) ([]Record, error) {
	root := opts.Root
	if root == "" {
		r, err := CursorDefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	dbPath := filepath.Join(root, "globalStorage", "state.vscdb")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	// mode=ro so a running Cursor is never disturbed, and its committed
	// WAL data is still visible.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("agentdx: open Cursor store: %w", err)
	}
	defer func() { _ = db.Close() }()

	if !hasTable(db, "cursorDiskKV") {
		return nil, fmt.Errorf("%w: no cursorDiskKV table in %s", ErrCursorSchema, dbPath)
	}

	rows, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE 'bubbleId:%'`)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCursorSchema, err)
	}
	defer func() { _ = rows.Close() }()

	var (
		out     []Record
		scanned int
	)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		scanned++
		var b cursorBubble
		if json.Unmarshal([]byte(value), &b) != nil {
			continue
		}
		if b.CreatedAt <= 0 {
			continue
		}
		at := time.UnixMilli(b.CreatedAt).UTC()
		if !opts.Since.IsZero() && at.Before(opts.Since) {
			continue
		}
		rec := Record{At: at, SessionID: composerFromKey(key)}

		switch {
		case b.ToolFormerData != nil && b.ToolFormerData.Name != "":
			rec.Kind = KindToolUse
			rec.ToolName = b.ToolFormerData.Name
		case b.Type == cursorBubbleUser:
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			rec.Kind = KindPrompt
		case b.Type == cursorBubbleAssistant:
			rec.Kind = KindAssistantTurn
			rec.InputTokens = b.TokenCount.InputTokens
		default:
			continue
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCursorSchema, err)
	}
	// Rows keyed as bubbles that none of them parsed into a known role
	// means the value shape moved, not that the operator was idle.
	if scanned > 0 && len(out) == 0 {
		return nil, fmt.Errorf("%w: %d bubble rows in %s, none in a known shape",
			ErrCursorSchema, scanned, dbPath)
	}
	return out, nil
}

// composerFromKey pulls the session id out of bubbleId:<composer>:<bubble>.
func composerFromKey(key string) string {
	parts := strings.Split(key, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// hasTable reports whether the database defines the named table.
func hasTable(db *sql.DB, name string) bool {
	var found string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	return err == nil && found == name
}
