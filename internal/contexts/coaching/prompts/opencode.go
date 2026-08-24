package prompts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // read-only driver for opencode.db
)

// OpencodeDefaultPath returns the conventional opencode store.
func OpencodeDefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}

// extractOpencode reads operator prompts out of opencode's SQLite store.
//
// The text lives on a part row joined to its message; the message's role
// says whether the operator or the model wrote it.
//
// opencode injects its own continuation prompts — "Continue if you have
// next steps" and similar — and marks them synthetic. Those are the agent
// prodding itself, and coaching someone on words they never wrote would
// be worse than not coaching them at all, so they are dropped.
func extractOpencode(path string, opts ExtractOptions) ([]UserPrompt, error) {
	if path == "" {
		p, err := OpencodeDefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("prompts: open opencode store: %w", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT m.session_id, m.data, p.data
		FROM part p JOIN message m ON m.id = p.message_id`)
	if err != nil {
		return nil, fmt.Errorf("prompts: query opencode store: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []UserPrompt
	for rows.Next() {
		var sessionID, msgData, partData string
		if err := rows.Scan(&sessionID, &msgData, &partData); err != nil {
			continue
		}
		var m struct {
			Role string `json:"role"`
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
		}
		if json.Unmarshal([]byte(msgData), &m) != nil || m.Role != "user" {
			continue
		}
		var p struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Synthetic bool   `json:"synthetic"`
		}
		if json.Unmarshal([]byte(partData), &p) != nil {
			continue
		}
		if p.Type != "text" || p.Synthetic || strings.TrimSpace(p.Text) == "" {
			continue
		}
		at := time.UnixMilli(m.Time.Created).UTC()
		if !opts.Since.IsZero() && at.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && at.After(opts.Until) {
			continue
		}
		if opts.SessionID != "" && sessionID != opts.SessionID {
			continue
		}
		out = append(out, UserPrompt{
			Timestamp: at,
			SessionID: sessionID,
			Text:      p.Text,
		})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, rows.Err()
}
