package prompts

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func seedOpencode(t *testing.T, rows [][4]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, data TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	for i, r := range rows {
		// r = messageID, sessionID, messageJSON, partJSON
		if _, err := db.Exec(`INSERT OR IGNORE INTO message (id, session_id, data) VALUES (?, ?, ?)`,
			r[0], r[1], r[2]); err != nil {
			t.Fatalf("msg: %v", err)
		}
		if r[3] == "" {
			continue
		}
		if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, data) VALUES (?, ?, ?, ?)`,
			string(rune('a'+i)), r[0], r[1], r[3]); err != nil {
			t.Fatalf("part: %v", err)
		}
	}
	return path
}

// opencode keeps prompt text in a part row joined to a user message.
func TestOpencodeExtractsUserPrompts(t *testing.T) {
	path := seedOpencode(t, [][4]string{
		{"m1", "s1", `{"role":"user","time":{"created":1771056604952}}`,
			`{"type":"text","text":"refactor the retry loop"}`},
	})
	got, err := extractOpencode(path, ExtractOptions{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 1 || got[0].Text != "refactor the retry loop" {
		t.Errorf("got %+v, want the typed prompt", got)
	}
}

// opencode injects continuation prompts and marks them synthetic. They
// are the agent prodding itself, not the operator writing — counting
// them would coach someone on words they never wrote.
func TestOpencodeSkipsSyntheticPrompts(t *testing.T) {
	path := seedOpencode(t, [][4]string{
		{"m1", "s1", `{"role":"user","time":{"created":1771056604952}}`,
			`{"type":"text","text":"Continue if you have next steps","synthetic":true}`},
		{"m2", "s1", `{"role":"user","time":{"created":1771056605952}}`,
			`{"type":"text","text":"now ship it"}`},
	})
	got, _ := extractOpencode(path, ExtractOptions{})
	if len(got) != 1 || got[0].Text != "now ship it" {
		t.Errorf("got %+v, want only the operator's own prompt", got)
	}
}

// Assistant text is a reply, not a prompt.
func TestOpencodeIgnoresAssistantText(t *testing.T) {
	path := seedOpencode(t, [][4]string{
		{"m1", "s1", `{"role":"assistant","time":{"created":1771056604952}}`,
			`{"type":"text","text":"here is the change"}`},
	})
	got, _ := extractOpencode(path, ExtractOptions{})
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing — assistant text is a reply", got)
	}
}

// A missing store is not an error: most operators do not run opencode.
func TestOpencodeAbsentIsNotAnError(t *testing.T) {
	got, err := extractOpencode(filepath.Join(t.TempDir(), "nope.db"), ExtractOptions{})
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v err=%v, want empty and no error", got, err)
	}
}
