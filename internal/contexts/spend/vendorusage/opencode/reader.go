// Package opencode reads opencode's local session store — a SQLite database
// at ~/.local/share/opencode/opencode.db — and surfaces per-assistant-turn
// token usage as TokenOps PromptEvents. Unlike the Claude Code and Codex
// readers (which parse JSONL files), opencode persists sessions in SQLite;
// this reader opens that database read-only so it never contends with a
// running opencode process.
package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, already a TokenOps dependency

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Turn is one assistant message extracted from the opencode message table.
type Turn struct {
	ID           string // message primary key — the dedup + deterministic-id source
	SessionID    string
	Project      string // derived from the session's working directory
	Model        string // opencode modelID (e.g. "claude-opus-4.6")
	Provider     eventschema.Provider
	InputTokens  int // uncached input + cache read + cache write
	CachedTokens int // cache read
	OutputTokens int // output + reasoning
	Cost         float64
	Timestamp    time.Time
}

// messageData mirrors the JSON persisted in opencode's message.data column
// for assistant turns. Only the fields TokenOps needs are modelled; opencode
// may add others without breaking this reader.
type messageData struct {
	Role       string  `json:"role"`
	ModelID    string  `json:"modelID"`
	ProviderID string  `json:"providerID"`
	Cost       float64 `json:"cost"`
	Time       struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Path struct {
		CWD  string `json:"cwd"`
		Root string `json:"root"`
	} `json:"path"`
	Tokens struct {
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

// DefaultRoot returns the default opencode database path, honouring
// XDG_DATA_HOME when set.
func DefaultRoot() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "opencode.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}

// ReadMessages opens dbPath read-only and invokes visit for every assistant
// turn that carries token usage. A missing database is not an error (opencode
// may not be installed) — visit simply isn't called.
func ReadMessages(dbPath string, visit func(Turn) error) error {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// mode=ro opens read-only and still reads committed WAL data, so a live
	// opencode process is never blocked and its uncommitted writes are
	// invisible until flushed.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open opencode db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, session_id, data FROM message`)
	if err != nil {
		return fmt.Errorf("query opencode messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, sessionID, data string
		if err := rows.Scan(&id, &sessionID, &data); err != nil {
			return err
		}
		var d messageData
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			// A single malformed row must not abort the whole scan.
			continue
		}
		if d.Role != "assistant" {
			continue
		}
		total := d.Tokens.Input + d.Tokens.Output + d.Tokens.Reasoning +
			d.Tokens.Cache.Read + d.Tokens.Cache.Write
		if total == 0 {
			continue
		}
		if err := visit(Turn{
			ID:           id,
			SessionID:    sessionID,
			Project:      projectFromPath(d.Path.Root, d.Path.CWD),
			Model:        d.ModelID,
			Provider:     mapProvider(d.ProviderID),
			InputTokens:  d.Tokens.Input + d.Tokens.Cache.Read + d.Tokens.Cache.Write,
			CachedTokens: d.Tokens.Cache.Read,
			OutputTokens: d.Tokens.Output + d.Tokens.Reasoning,
			Cost:         d.Cost,
			Timestamp:    time.UnixMilli(d.Time.Created).UTC(),
		}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// projectFromPath derives a stable project label from the session's working
// directory: the git root's basename when it is a real path, otherwise the
// cwd's basename.
func projectFromPath(root, cwd string) string {
	if root != "" && root != "/" {
		return filepath.Base(root)
	}
	if cwd != "" {
		return filepath.Base(cwd)
	}
	return "unknown"
}

// mapProvider normalizes opencode's providerID to a TokenOps provider. The
// Provider type is an open string, so unknown providers pass through verbatim
// rather than collapsing to "unknown".
func mapProvider(providerID string) eventschema.Provider {
	switch providerID {
	case "anthropic":
		return eventschema.ProviderAnthropic
	case "openai":
		return eventschema.ProviderOpenAI
	case "github-copilot", "github":
		return eventschema.ProviderGitHub
	case "google", "gemini", "google-vertex":
		return eventschema.ProviderGemini
	case "openrouter":
		return eventschema.ProviderOpenRouter
	case "":
		return eventschema.ProviderUnknown
	default:
		return eventschema.Provider(providerID)
	}
}
