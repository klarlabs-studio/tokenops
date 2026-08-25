package agentdx

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // read-only driver for opencode.db
)

// ErrOpencodeSchema reports that an opencode database was found but did
// not have the tables this reader understands. Distinct from an empty
// result for the same reason as the Cursor reader: a store that cannot be
// read must not be reported as an operator who did no work.
var ErrOpencodeSchema = errors.New("agentdx: unrecognised opencode database schema")

// OpencodeDefaultPath returns the conventional opencode store.
func OpencodeDefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}

// opencodeMessage is the JSON blob on a message row.
type opencodeMessage struct {
	Role string `json:"role"`
	Time struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	// ProviderID is set on assistant rows; user rows carry it nested
	// under model instead.
	ProviderID string `json:"providerID"`
	Model      struct {
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Tokens struct {
		Input  int64 `json:"input"`
		Output int64 `json:"output"`
		Cache  struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

// opencodePart is the JSON blob on a part row.
type opencodePart struct {
	Type  string `json:"type"`
	Tool  string `json:"tool"`
	Text  string `json:"text"`
	State struct {
		Input    json.RawMessage `json:"input"`
		InputRef struct {
			FilePath string `json:"filePath"`
			Path     string `json:"path"`
		} `json:"-"`
	} `json:"state"`
}

// ExtractOpencode reads opencode's SQLite store into Records.
//
// opencode is the richest of the supported clients and the only
// multi-provider one: every turn records which upstream served it, so it
// is the only place the question "is OpenRouter a worse experience than
// Anthropic" can even be asked. It also records compactions explicitly
// rather than leaving them to be inferred from a summary row.
func ExtractOpencode(opts ExtractOptions) ([]Record, error) {
	path := opts.Root
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
		return nil, fmt.Errorf("agentdx: open opencode store: %w", err)
	}
	defer func() { _ = db.Close() }()

	if !hasTable(db, "message") {
		return nil, fmt.Errorf("%w: no message table in %s", ErrOpencodeSchema, path)
	}

	// Instruction text lives on a part row, but the prompt record is
	// emitted from the message row — so which messages reject has to be
	// known before the messages are walked.
	rejecting := map[string]bool{}
	if hasTable(db, "part") {
		rejecting = opencodeRejectingMessages(db)
	}

	out, byMessage, err := opencodeMessages(db, opts.Since, rejecting)
	if err != nil {
		return nil, err
	}
	// The part table is optional: a store can hold messages before any
	// tool has run. Its absence is not a schema failure.
	if hasTable(db, "part") {
		parts, err := opencodeParts(db, byMessage, opts.Since)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

// messageContext is what a part needs from the message it belongs to.
type messageContext struct {
	at        time.Time
	sessionID string
	provider  string
}

// opencodeRejectingMessages returns the ids of user messages whose text
// rejects the answer before it. A query failure yields an empty set
// rather than an error: a missing rejection signal degrades one metric,
// where failing the whole read would lose all of them.
func opencodeRejectingMessages(db *sql.DB) map[string]bool {
	out := map[string]bool{}
	rows, err := db.Query(`SELECT message_id, data FROM part`)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var messageID, data string
		if rows.Scan(&messageID, &data) != nil {
			continue
		}
		var p opencodePart
		if json.Unmarshal([]byte(data), &p) != nil || p.Type != "text" {
			continue
		}
		if IsRejection(p.Text) {
			out[messageID] = true
		}
	}
	return out
}

func opencodeMessages(db *sql.DB, since time.Time, rejecting map[string]bool) ([]Record, map[string]messageContext, error) {
	rows, err := db.Query(`SELECT id, session_id, data FROM message`)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrOpencodeSchema, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	byMessage := map[string]messageContext{}
	for rows.Next() {
		var id, sessionID, data string
		if err := rows.Scan(&id, &sessionID, &data); err != nil {
			continue
		}
		var m opencodeMessage
		if json.Unmarshal([]byte(data), &m) != nil || m.Time.Created <= 0 {
			continue
		}
		at := time.UnixMilli(m.Time.Created).UTC()
		provider := m.ProviderID
		if provider == "" {
			provider = m.Model.ProviderID
		}
		byMessage[id] = messageContext{at: at, sessionID: sessionID, provider: provider}

		if !since.IsZero() && at.Before(since) {
			continue
		}
		rec := Record{At: at, SessionID: sessionID, Provider: provider}
		switch m.Role {
		case "user":
			rec.Kind = KindPrompt
			rec.Rejects = rejecting[id]
		case "assistant":
			rec.Kind = KindAssistantTurn
			rec.InputTokens = m.Tokens.Input + m.Tokens.Cache.Read + m.Tokens.Cache.Write
		default:
			continue
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrOpencodeSchema, err)
	}
	return out, byMessage, nil
}

func opencodeParts(db *sql.DB, byMessage map[string]messageContext, since time.Time) ([]Record, error) {
	rows, err := db.Query(`SELECT message_id, data FROM part`)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpencodeSchema, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	for rows.Next() {
		var messageID, data string
		if err := rows.Scan(&messageID, &data); err != nil {
			continue
		}
		var p opencodePart
		if json.Unmarshal([]byte(data), &p) != nil {
			continue
		}
		// A part inherits its message's time and session; its own
		// timestamp columns track edits to the row rather than when the
		// work happened.
		ctx, ok := byMessage[messageID]
		if !ok {
			continue
		}
		if !since.IsZero() && ctx.at.Before(since) {
			continue
		}
		rec := Record{At: ctx.at, SessionID: ctx.sessionID, Provider: ctx.provider}
		switch p.Type {
		case "tool":
			rec.Kind = KindToolUse
			rec.ToolName = p.Tool
			rec.FilePath = opencodeToolPath(p.State.Input)
			rec.CallSignature = callSignature(p.Tool, p.State.Input)
		case "compaction":
			rec.Kind = KindCompaction
		default:
			continue
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpencodeSchema, err)
	}
	return out, nil
}

// opencodeToolPath pulls the edited file out of a tool call's arguments.
func opencodeToolPath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	return firstNonEmpty(in.FilePath, in.Path)
}
