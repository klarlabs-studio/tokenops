package replies

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
		if _, err := db.Exec(`INSERT OR IGNORE INTO message (id, session_id, data) VALUES (?, ?, ?)`,
			r[0], r[1], r[2]); err != nil {
			t.Fatalf("msg: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, data) VALUES (?, ?, ?, ?)`,
			string(rune('a'+i)), r[0], r[1], r[3]); err != nil {
			t.Fatalf("part: %v", err)
		}
	}
	return path
}

// The reply coach measures the model's output density. opencode keeps
// that text on a part row joined to an assistant message.
func TestOpencodeExtractsAssistantText(t *testing.T) {
	path := seedOpencode(t, [][4]string{
		{"m1", "s1", `{"role":"assistant","time":{"created":1771056604952}}`,
			`{"type":"text","text":"Refactored the retry loop and added a test."}`},
	})
	got, err := extractOpencode(path, ExtractOptions{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 1 || got[0].Text != "Refactored the retry loop and added a test." {
		t.Errorf("got %+v, want the assistant's reply", got)
	}
}

// The operator's own prompt is not a reply; measuring it as one would
// blend the human's writing into a report about the model's.
func TestOpencodeIgnoresUserText(t *testing.T) {
	path := seedOpencode(t, [][4]string{
		{"m1", "s1", `{"role":"user","time":{"created":1771056604952}}`,
			`{"type":"text","text":"do the thing"}`},
	})
	if got, _ := extractOpencode(path, ExtractOptions{}); len(got) != 0 {
		t.Errorf("got %+v, want nothing for a user turn", got)
	}
}

// Reasoning is the model thinking, not the prose it showed the operator.
// Counting it would make every reply look far longer than what was read.
func TestOpencodeIgnoresReasoningParts(t *testing.T) {
	path := seedOpencode(t, [][4]string{
		{"m1", "s1", `{"role":"assistant","time":{"created":1771056604952}}`,
			`{"type":"reasoning","text":"considering the options at length"}`},
	})
	if got, _ := extractOpencode(path, ExtractOptions{}); len(got) != 0 {
		t.Errorf("got %+v, want nothing for a reasoning part", got)
	}
}

// A missing store is not an error.
func TestOpencodeAbsentIsNotAnError(t *testing.T) {
	got, err := extractOpencode(filepath.Join(t.TempDir(), "nope.db"), ExtractOptions{})
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v err=%v, want empty and no error", got, err)
	}
}
