package agentdx

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// seedOpencodeDB writes opencode's real schema: a message table and a
// part table joined by message_id.
func seedOpencodeDB(t *testing.T, msgs, parts [][3]any, withPart bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT,
		time_created INTEGER, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatalf("create message: %v", err)
	}
	for _, m := range msgs {
		// time_created is not read back — the reader takes the time from
		// the JSON blob, which is where opencode actually keeps it.
		if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, data)
			VALUES (?, ?, 0, ?)`, m[0], m[1], m[2]); err != nil {
			t.Fatalf("insert msg: %v", err)
		}
	}
	if withPart {
		if _, err := db.Exec(`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT,
			session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)`); err != nil {
			t.Fatalf("create part: %v", err)
		}
		for _, p := range parts {
			if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, data)
				VALUES (?, ?, ?, ?, ?)`, p[0], p[1], "s1", int64(1), p[2]); err != nil {
				t.Fatalf("insert part: %v", err)
			}
		}
	}
	return path
}

// opencode records the operator's message and the model's reply as
// separate rows, with real token counts and a completion time — the
// richest of the three clients, and the only one carrying which provider
// each request went to.
func TestOpencodeExtractsPromptsTurnsAndProvider(t *testing.T) {
	path := seedOpencodeDB(t, [][3]any{
		{"m1", "s1", `{"role":"user","time":{"created":1771056604952},"model":{"providerID":"openrouter"}}`},
		{"m2", "s1", `{"role":"assistant","time":{"created":1771056613283,"completed":1771056645057},` +
			`"providerID":"openrouter","tokens":{"input":63976,"output":1013,"cache":{"read":100,"write":0}}}`},
	}, nil, false)

	got, err := ExtractOpencode(ExtractOptions{Root: path})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	var prompt, turn *Record
	for i := range got {
		switch got[i].Kind {
		case KindPrompt:
			prompt = &got[i]
		case KindAssistantTurn:
			turn = &got[i]
		}
	}
	if prompt == nil || turn == nil {
		t.Fatalf("want a prompt and a turn, got %+v", got)
	}
	if turn.InputTokens != 64076 {
		t.Errorf("InputTokens = %d, want 64076 (input + cache read)", turn.InputTokens)
	}
	if turn.Provider != "openrouter" {
		t.Errorf("Provider = %q, want openrouter — the only client that records it", turn.Provider)
	}
}

// Tool calls live in a separate table joined by message_id.
func TestOpencodeExtractsToolCalls(t *testing.T) {
	path := seedOpencodeDB(t,
		[][3]any{{"m1", "s1", `{"role":"assistant","time":{"created":1771056613283},"providerID":"openai"}`}},
		[][3]any{{"p1", "m1", `{"type":"tool","tool":"edit"}`}},
		true)

	got, _ := ExtractOpencode(ExtractOptions{Root: path})
	var tools int
	for _, r := range got {
		if r.Kind == KindToolUse && r.ToolName == "edit" {
			tools++
		}
	}
	if tools != 1 {
		t.Errorf("tool records = %d, want 1: %+v", tools, got)
	}
}

// opencode records compactions explicitly, so they need no inference at
// all — unlike Claude Code, where the marker sits on a user row.
func TestOpencodeExtractsCompaction(t *testing.T) {
	path := seedOpencodeDB(t,
		[][3]any{{"m1", "s1", `{"role":"assistant","time":{"created":1771056613283}}`}},
		[][3]any{{"p1", "m1", `{"type":"compaction"}`}},
		true)

	got, _ := ExtractOpencode(ExtractOptions{Root: path})
	var compactions int
	for _, r := range got {
		if r.Kind == KindCompaction {
			compactions++
		}
	}
	if compactions != 1 {
		t.Errorf("compactions = %d, want 1", compactions)
	}
}

// A store whose schema moved must say so rather than report an idle
// operator — the same rule the Cursor reader follows.
func TestOpencodeUnknownSchemaIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	db, _ := sql.Open("sqlite", path)
	if _, err := db.Exec(`CREATE TABLE something_else (x TEXT)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = db.Close()

	if _, err := ExtractOpencode(ExtractOptions{Root: path}); !errors.Is(err, ErrOpencodeSchema) {
		t.Errorf("err = %v, want ErrOpencodeSchema", err)
	}
}

// Absent opencode is not an error.
func TestOpencodeAbsentIsNotAnError(t *testing.T) {
	got, err := ExtractOpencode(ExtractOptions{Root: filepath.Join(t.TempDir(), "nope.db")})
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v err=%v, want empty and no error", got, err)
	}
}
