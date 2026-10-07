// Package opencodedb reads opencode's local store, in both of its shapes.
//
// opencode 1.x keeps messages in `message` and their text, tool calls and
// compactions in `part`. opencode 2 keeps everything in `session_message`,
// one row per message with its type in a column and its content inline,
// and the session's directory in `session_v2` (opencode 1.18 creates
// session_message first, with sessions still in `session`). Both versions use the same
// opencode.db, and 2.x copies 1.x sessions across lazily, keeping their
// message IDs, while 1.x keeps writing the old tables. So during the
// transition both shapes hold live data: every message is read from
// whichever table has it, the 2.x row winning, and none is counted twice.
//
// Every TokenOps reader of opencode (spend, agent DX, prompt and reply
// coaching, the source probe) reads through the Reader port, so the two
// shapes are handled once. This package holds what a message is; the SQL
// that reads either shape is internal/infra/opencodedb's.
package opencodedb

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ErrSchema reports a store that exists but has neither shape's tables.
// It is distinct from an empty result: a store that cannot be read must
// not be reported as an operator who did no work.
var ErrSchema = errors.New("opencodedb: unrecognised opencode database schema")

// DefaultPath is opencode's database: OPENCODE_DB when set (opencode 2
// honours it), else under XDG_DATA_HOME, else ~/.local/share.
func DefaultPath() (string, error) {
	if p := os.Getenv("OPENCODE_DB"); p != "" {
		return p, nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "opencode.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}

// Role is what a message is.
type Role string

// Roles.
const (
	User       Role = "user"
	Assistant  Role = "assistant"
	Compaction Role = "compaction"
)

// Tokens is a turn's usage.
type Tokens struct {
	Input, Output, Reasoning, CacheRead, CacheWrite int64
}

// Tool is one tool call on an assistant turn.
type Tool struct {
	Name  string
	Input json.RawMessage
}

// Path is the file a tool call worked on: 1.x calls it filePath, 2.x
// path, and a 1.x call migrated to 2.x keeps filePath.
func (t Tool) Path() string {
	if len(t.Input) == 0 {
		return ""
	}
	var in struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(t.Input, &in) != nil {
		return ""
	}
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.Path
}

// Message is one message, in either shape.
type Message struct {
	ID, SessionID string
	Role          Role
	Created       time.Time
	ProviderID    string
	ModelID       string
	// Variant is the reasoning effort the turn ran at, when the model
	// offers one.
	Variant string
	Cost    float64
	Tokens  Tokens
	// Root and CWD are the session's project directory and working
	// directory. 2.x records one directory per session, given as both.
	Root, CWD string
	// Text is the message's prose: the instruction on a user message,
	// the reply on an assistant one. Synthetic text (opencode prodding
	// itself) and reasoning are left out. Set only with Options.Parts.
	Text []string
	// Tools are an assistant turn's tool calls. Set only with
	// Options.Parts.
	Tools []Tool
}

// Options narrows a read.
type Options struct {
	// SessionID reads one session only.
	SessionID string
	// Since skips messages created before it.
	Since time.Time
	// Parts reads text, tool calls and compactions as well as messages.
	// In 1.x they live in a separate, much larger table.
	Parts bool
}
