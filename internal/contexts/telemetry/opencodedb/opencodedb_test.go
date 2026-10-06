package opencodedb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func ms(d time.Duration) int64 { return t0.Add(d).UnixMilli() }

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

// mixedStore is a store mid-transition: 1.x rows for an old session, a
// 2.x copy of one of them (same ID), and a 2.x-only session.
func mixedStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`)
	exec(`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT)`)
	exec(`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, directory TEXT NOT NULL)`)
	exec(`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`)

	// 1.x: an instruction, a turn with a read, a synthetic nudge, a compaction.
	exec(`INSERT INTO message VALUES ('u1','s1',?,?)`, ms(0), js(map[string]any{"role": "user", "time": map[string]any{"created": ms(0)}}))
	exec(`INSERT INTO part VALUES ('p1','u1',?)`, js(map[string]any{"type": "text", "text": "fix the bug"}))
	exec(`INSERT INTO part VALUES ('p2','u1',?)`, js(map[string]any{"type": "text", "text": "Continue if you have next steps", "synthetic": true}))
	exec(`INSERT INTO message VALUES ('a1','s1',?,?)`, ms(time.Minute), js(map[string]any{
		"role": "assistant", "providerID": "zai-coding-plan", "modelID": "glm-5.3", "variant": "high", "cost": 0,
		"time": map[string]any{"created": ms(time.Minute)}, "path": map[string]any{"root": "/w/proj", "cwd": "/w/proj/sub"},
		"tokens": map[string]any{"input": 100, "output": 10, "reasoning": 5, "cache": map[string]any{"read": 50, "write": 0}},
	}))
	exec(`INSERT INTO part VALUES ('p3','a1',?)`, js(map[string]any{"type": "tool", "tool": "read", "state": map[string]any{"input": map[string]any{"filePath": "/w/proj/a.go"}}}))
	exec(`INSERT INTO part VALUES ('p4','a1',?)`, js(map[string]any{"type": "compaction"}))

	// 2.x copied a1 across (same ID, 2.x row wins) and holds a session
	// of its own.
	exec(`INSERT INTO session_v2 VALUES ('s1','/w/proj'), ('s2','/w/other')`)
	exec(`INSERT INTO session_message VALUES ('a1','s1','assistant',2,?,?,?)`, ms(time.Minute), ms(time.Minute), js(map[string]any{
		"model": map[string]any{"providerID": "zai-coding-plan", "id": "glm-5.3", "variant": "high"}, "cost": 0,
		"tokens":  map[string]any{"input": 100, "output": 10, "reasoning": 5, "cache": map[string]any{"read": 50, "write": 0}},
		"content": []any{map[string]any{"type": "tool", "name": "read", "state": map[string]any{"input": map[string]any{"filePath": "/w/proj/a.go"}}}},
	}))
	exec(`INSERT INTO session_message VALUES ('u2','s2','user',1,?,?,?)`, ms(time.Hour), ms(time.Hour), js(map[string]any{"text": "add a test"}))
	exec(`INSERT INTO session_message VALUES ('x2','s2','synthetic',2,?,?,?)`, ms(time.Hour), ms(time.Hour), js(map[string]any{"text": "continue"}))
	exec(`INSERT INTO session_message VALUES ('a2','s2','assistant',3,?,?,?)`, ms(time.Hour+time.Minute), ms(time.Hour+time.Minute), js(map[string]any{
		"model": map[string]any{"providerID": "opencode-go", "id": "kimi-k3"}, "cost": 0.07,
		"tokens": map[string]any{"input": 200, "output": 20, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}},
		"content": []any{
			map[string]any{"type": "text", "text": "done"},
			map[string]any{"type": "reasoning", "text": "thinking"},
			map[string]any{"type": "tool", "name": "edit", "state": map[string]any{"input": map[string]any{"path": "/w/other/b.go"}}},
		},
	}))
	exec(`INSERT INTO session_message VALUES ('c2','s2','compaction',4,?,?,?)`, ms(2*time.Hour), ms(2*time.Hour), js(map[string]any{"status": "completed", "reason": "auto"}))
	return path
}

func readAll(t *testing.T, path string, opts Options) map[string]Message {
	t.Helper()
	out := map[string]Message{}
	if err := Read(path, opts, func(m Message) error {
		if _, dup := out[m.ID]; dup {
			t.Fatalf("message %s read twice", m.ID)
		}
		out[m.ID] = m
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadsBothShapesOnce(t *testing.T) {
	got := readAll(t, mixedStore(t), Options{Parts: true})
	// a1's 1.x compaction part is not read: 2.x copies a session's
	// compactions as messages of their own, so the copy owns them.
	want := map[string]Role{"u1": User, "a1": Assistant, "u2": User, "a2": Assistant, "c2": Compaction}
	if len(got) != len(want) {
		t.Fatalf("read %v", keys(got))
	}
	for id, role := range want {
		if got[id].Role != role {
			t.Errorf("%s: role %q, want %q", id, got[id].Role, role)
		}
	}
	if _, ok := got["x2"]; ok {
		t.Error("a 2.x synthetic message was read as the operator's")
	}
	if u1 := got["u1"]; len(u1.Text) != 1 || u1.Text[0] != "fix the bug" {
		t.Errorf("1.x instruction text %q; synthetic parts are not the operator's", u1.Text)
	}
	if u2 := got["u2"]; len(u2.Text) != 1 || u2.Text[0] != "add a test" {
		t.Errorf("2.x instruction text %q", u2.Text)
	}
	a1 := got["a1"]
	if a1.ProviderID != "zai-coding-plan" || a1.ModelID != "glm-5.3" || a1.Variant != "high" || a1.Tokens.CacheRead != 50 || a1.Root != "/w/proj" {
		t.Errorf("a1 (the 2.x copy) = %+v", a1)
	}
	if len(a1.Tools) != 1 || a1.Tools[0].Path() != "/w/proj/a.go" {
		t.Errorf("a1 tools %+v; a migrated call keeps filePath", a1.Tools)
	}
	a2 := got["a2"]
	if a2.Cost != 0.07 || a2.Root != "/w/other" || len(a2.Text) != 1 || a2.Text[0] != "done" {
		t.Errorf("a2 = %+v; reasoning is not reply text", a2)
	}
	if len(a2.Tools) != 1 || a2.Tools[0].Path() != "/w/other/b.go" {
		t.Errorf("a2 tools %+v; 2.x calls name the file path", a2.Tools)
	}
}

// A 1.x message 2.x has not copied keeps its compaction.
func TestOneShapeCompactionStillCounts(t *testing.T) {
	path := mixedStore(t)
	db, _ := sql.Open("sqlite", path)
	_, _ = db.Exec(`DELETE FROM session_message WHERE id = 'a1'`)
	_ = db.Close()
	if got := readAll(t, path, Options{Parts: true}); got["a1#compaction"].Role != Compaction {
		t.Errorf("1.x compaction missing: %v", keys(got))
	}
}

func TestReadNarrows(t *testing.T) {
	path := mixedStore(t)
	if got := readAll(t, path, Options{SessionID: "s2"}); len(got) != 3 {
		t.Errorf("session s2: %v", keys(got))
	}
	if got := readAll(t, path, Options{Since: t0.Add(30 * time.Minute)}); len(got) != 3 {
		t.Errorf("since: %v", keys(got))
	}
	if got := readAll(t, path, Options{}); got["a2"].Tools != nil || got["u2"].Text != nil {
		t.Error("parts were read without Options.Parts")
	}
}

func TestNewestAcrossShapes(t *testing.T) {
	got, ok := Newest(mixedStore(t))
	if !ok || !got.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("Newest = %v %v; an upgraded opencode must not look idle", got, ok)
	}
}

func TestSchemaAndAbsence(t *testing.T) {
	if err := Read(filepath.Join(t.TempDir(), "missing.db"), Options{}, func(Message) error { return nil }); err != nil {
		t.Errorf("a missing store is not an error: %v", err)
	}
	path := filepath.Join(t.TempDir(), "other.db")
	db, _ := sql.Open("sqlite", path)
	_, _ = db.Exec(`CREATE TABLE something_else (x TEXT)`)
	_ = db.Close()
	if err := Read(path, Options{}, func(Message) error { return nil }); !errors.Is(err, ErrSchema) {
		t.Errorf("err = %v, want ErrSchema", err)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("OPENCODE_DB", "/x/opencode.db")
	if p, _ := DefaultPath(); p != "/x/opencode.db" {
		t.Errorf("OPENCODE_DB: %q", p)
	}
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if p, _ := DefaultPath(); p != "/xdg/opencode/opencode.db" {
		t.Errorf("XDG: %q", p)
	}
}

func keys(m map[string]Message) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// opencode 1.18 creates session_message before it moves sessions to
// session_v2: the new table is there, empty or not, beside the 1.x tables,
// and its sessions live in `session`. Joining session_v2 failed the whole
// read, so every opencode session went missing from every figure.
func TestSessionMessageBesideTheOldSessionTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT NOT NULL)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session VALUES ('s1','/w/proj'), ('s3','/w/new')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO message VALUES ('a1','s1',?,?)`, ms(time.Minute), js(map[string]any{
		"role": "assistant", "providerID": "anthropic", "modelID": "claude-opus-5", "time": map[string]any{"created": ms(time.Minute)},
		"tokens": map[string]any{"input": 100, "output": 10},
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message VALUES ('a3','s3','assistant',1,?,?,?)`, ms(time.Hour), ms(time.Hour), js(map[string]any{
		"model": map[string]any{"providerID": "anthropic", "id": "claude-opus-5"}, "tokens": map[string]any{"input": 7, "output": 3},
	})); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	got := readAll(t, path, Options{})
	if len(got) != 2 {
		t.Fatalf("read %v, want the 1.x and the session_message row", keys(got))
	}
	if got["a3"].Root != "/w/new" {
		t.Errorf("session_message row's directory = %q, want it from session", got["a3"].Root)
	}
	if got["a1"].ModelID != "claude-opus-5" {
		t.Errorf("1.x row = %+v", got["a1"])
	}
}
