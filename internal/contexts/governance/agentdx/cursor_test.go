package agentdx

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// seedCursorDB writes a state.vscdb with Cursor's documented shape.
func seedCursorDB(t *testing.T, rows map[string]string, table string) string {
	t.Helper()
	dir := t.TempDir()
	global := filepath.Join(dir, "globalStorage")
	if err := os.MkdirAll(global, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(global, "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE ` + table + ` (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for k, v := range rows {
		if _, err := db.Exec(`INSERT INTO `+table+` (key, value) VALUES (?, ?)`, k, v); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return dir
}

// Cursor keys each message as bubbleId:<composer>:<bubble> in cursorDiskKV.
// A user bubble opens a unit of work; an assistant bubble is a turn.
func TestCursorExtractsPromptsAndTurns(t *testing.T) {
	root := seedCursorDB(t, map[string]string{
		"bubbleId:c1:b1": `{"type":1,"text":"fix the retry path","createdAt":1756000000000}`,
		"bubbleId:c1:b2": `{"type":2,"text":"done","createdAt":1756000010000,"tokenCount":{"inputTokens":1200}}`,
	}, "cursorDiskKV")

	got, err := ExtractCursor(ExtractOptions{Root: root})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	var prompts, turns int
	for _, r := range got {
		switch r.Kind {
		case KindPrompt:
			prompts++
		case KindAssistantTurn:
			turns++
		}
	}
	if prompts != 1 || turns != 1 {
		t.Errorf("prompts=%d turns=%d, want 1 and 1: %+v", prompts, turns, got)
	}
}

// Bubbles from different composers are different sessions.
func TestCursorSessionsFromComposerID(t *testing.T) {
	root := seedCursorDB(t, map[string]string{
		"bubbleId:c1:b1": `{"type":1,"text":"a","createdAt":1756000000000}`,
		"bubbleId:c2:b1": `{"type":1,"text":"b","createdAt":1756000001000}`,
	}, "cursorDiskKV")

	got, _ := ExtractCursor(ExtractOptions{Root: root})
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.SessionID] = true
	}
	if len(seen) != 2 {
		t.Errorf("sessions = %v, want 2 distinct composers", seen)
	}
}

// THE thing today taught: a schema that does not match must fail loudly.
// Every metric that was silently wrong today — compactions at 0, the
// window at 0/200, TEU as "not measured" — read as an absence of data
// when it was really a failure to read it. Cursor's schema is
// undocumented and moves; a mismatch has to say so.
func TestCursorUnknownSchemaIsAnError(t *testing.T) {
	root := seedCursorDB(t, map[string]string{"someKey": "{}"}, "SomeOtherTable")

	_, err := ExtractCursor(ExtractOptions{Root: root})
	if err == nil {
		t.Fatal("an unrecognised schema must error, not report zero activity")
	}
	if !errors.Is(err, ErrCursorSchema) {
		t.Errorf("err = %v, want ErrCursorSchema so callers can tell it apart", err)
	}
}

// No Cursor at all is not an error — most operators do not run it, and
// reporting a failure would make the common case look broken.
func TestCursorAbsentIsNotAnError(t *testing.T) {
	got, err := ExtractCursor(ExtractOptions{Root: t.TempDir()})
	if err != nil {
		t.Errorf("absent Cursor should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// Since filters the window.
func TestCursorHonoursSince(t *testing.T) {
	root := seedCursorDB(t, map[string]string{
		"bubbleId:c1:b1": `{"type":1,"text":"old","createdAt":1000000000000}`,
		"bubbleId:c1:b2": `{"type":1,"text":"new","createdAt":1756000000000}`,
	}, "cursorDiskKV")

	got, _ := ExtractCursor(ExtractOptions{
		Root:  root,
		Since: time.UnixMilli(1700000000000),
	})
	if len(got) != 1 {
		t.Errorf("got %d records, want only the one inside the window", len(got))
	}
}
