package agentdx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeCursor is a CursorStore over rows in memory. The SQL that reads a
// real state.vscdb is internal/infra/cursorstate's, tested there; what
// the rows mean is tested here.
type fakeCursor struct {
	rows  map[string]string
	table string
}

func (f fakeCursor) Bubbles(path string, yield func(key, value string)) error {
	if f.table != "cursorDiskKV" {
		return fmt.Errorf("%w: no cursorDiskKV table in %s", ErrCursorSchema, path)
	}
	keys := make([]string, 0, len(f.rows))
	for k := range f.rows {
		if strings.HasPrefix(k, "bubbleId:") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		yield(k, f.rows[k])
	}
	return nil
}

// useCursor installs s as the Cursor reader for one test.
func useCursor(t *testing.T, s CursorStore) {
	t.Helper()
	prev := cursorStore
	UseCursorStore(s)
	t.Cleanup(func() { UseCursorStore(prev) })
}

// seedCursorDB lays out a Cursor user-data directory whose state.vscdb
// holds rows in table, served by a fakeCursor.
func seedCursorDB(t *testing.T, rows map[string]string, table string) string {
	t.Helper()
	dir := t.TempDir()
	global := filepath.Join(dir, "globalStorage")
	if err := os.MkdirAll(global, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(global, "state.vscdb"), nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	useCursor(t, fakeCursor{rows: rows, table: table})
	return dir
}

// A Cursor store on disk that this process cannot read is reported, never
// read as an operator who did not use Cursor.
func TestCursorWithoutAReaderIsReported(t *testing.T) {
	root := seedCursorDB(t, nil, "cursorDiskKV")
	useCursor(t, nil)
	if _, err := ExtractCursor(ExtractOptions{Root: root}); !errors.Is(err, ErrNoCursorStore) {
		t.Fatalf("err = %v, want ErrNoCursorStore", err)
	}
	// Auto mode names it alongside whatever else it read.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	cursorRoot, err := CursorDefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cursorRoot, "globalStorage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorRoot, "globalStorage", "state.vscdb"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractAll(ExtractOptions{}); !errors.Is(err, ErrNoCursorStore) {
		t.Fatalf("auto: err = %v, want ErrNoCursorStore", err)
	}
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

// Current Cursor builds write createdAt as a string; a reader that took
// only the number reported 269,031 real bubbles as an unknown schema.
func TestCursorCreatedAtAsString(t *testing.T) {
	root := seedCursorDB(t, map[string]string{
		"bubbleId:c1:b1": `{"_v":3,"type":1,"text":"fix the retry path","createdAt":"2026-10-02T09:00:00.000Z","tokenCount":{}}`,
		"bubbleId:c1:b2": `{"_v":3,"type":2,"text":"done","createdAt":"1759395610000","tokenCount":{"inputTokens":1200}}`,
		"bubbleId:c1:b3": `{"_v":3,"type":2,"text":"older","createdAt":1756000010000}`,
	}, "cursorDiskKV")

	recs, err := ExtractCursor(ExtractOptions{Root: root})
	if err != nil {
		t.Fatalf("string timestamps read as a schema failure: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("records %d, want 3", len(recs))
	}
	want := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	found := false
	for _, r := range recs {
		if r.Kind == KindPrompt && r.At.Equal(want) {
			found = true
		}
	}
	if !found {
		t.Errorf("the ISO createdAt was not parsed: %+v", recs)
	}
}

// A store whose bubbles all predate the window is an operator who did not
// use Cursor lately, not a schema failure.
func TestCursorOldBubblesAreNotASchemaFailure(t *testing.T) {
	root := seedCursorDB(t, map[string]string{
		"bubbleId:c1:b1": `{"type":1,"text":"old","createdAt":"2025-01-02T09:00:00Z"}`,
	}, "cursorDiskKV")
	recs, err := ExtractCursor(ExtractOptions{Root: root, Since: time.Now().AddDate(0, 0, -7)})
	if err != nil || len(recs) != 0 {
		t.Fatalf("recs %d, err %v; want none and no error", len(recs), err)
	}
}

func TestCursorTimestampShapes(t *testing.T) {
	want := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	for _, raw := range []string{
		`1790931600000`, `1790931600`, `"1790931600000"`, `"1790931600"`,
		`"2026-10-02T09:00:00Z"`, `"2026-10-02T09:00:00.000Z"`, `"2026-10-02 09:00:00"`,
	} {
		var ct cursorTime
		if err := ct.UnmarshalJSON([]byte(raw)); err != nil || !ct.at.Equal(want) {
			t.Errorf("%s: %v, want %v", raw, ct.at, want)
		}
	}
	var ct cursorTime
	_ = ct.UnmarshalJSON([]byte(`"next tuesday"`))
	if !ct.at.IsZero() || ct.raw != "next tuesday" {
		t.Errorf("unreadable: %+v", ct)
	}
}

func TestCursorSchemaErrorShowsTheTimestamp(t *testing.T) {
	root := seedCursorDB(t, map[string]string{
		"bubbleId:c1:b1": `{"type":1,"text":"x","createdAt":"next tuesday"}`,
	}, "cursorDiskKV")
	_, err := ExtractCursor(ExtractOptions{Root: root})
	if !errors.Is(err, ErrCursorSchema) || !strings.Contains(err.Error(), `"next tuesday"`) {
		t.Errorf("err = %v", err)
	}
}
