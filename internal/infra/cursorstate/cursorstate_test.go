package cursorstate

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// seed writes dir/state.vscdb with one table of key/value rows.
func seed(t *testing.T, dir, table string, rows map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE ` + table + ` (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	for k, v := range rows {
		if _, err := db.Exec(`INSERT INTO `+table+` (key, value) VALUES (?, ?)`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// Only message rows are handed over; the rest of the key-value store is
// Cursor's settings and is none of the reader's business.
func TestBubblesYieldsOnlyMessageRows(t *testing.T) {
	path := seed(t, t.TempDir(), "cursorDiskKV", map[string]string{
		"bubbleId:c1:b1":    `{"type":1}`,
		"bubbleId:c1:b2":    `{"type":2}`,
		"composerData:c1":   `{}`,
		"someSetting:theme": `"dark"`,
	})
	var keys []string
	if err := (Store{}).Bubbles(path, func(k, _ string) { keys = append(keys, k) }); err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "bubbleId:c1:b1" || keys[1] != "bubbleId:c1:b2" {
		t.Errorf("keys = %v", keys)
	}
}

// A store without the table is a schema Cursor moved, and must say so
// rather than read as an idle operator.
func TestBubblesUnknownSchemaIsAnError(t *testing.T) {
	path := seed(t, t.TempDir(), "SomeOtherTable", map[string]string{"someKey": "{}"})
	err := (Store{}).Bubbles(path, func(string, string) { t.Error("yielded a row from an unknown schema") })
	if !errors.Is(err, agentdx.ErrCursorSchema) {
		t.Errorf("err = %v, want ErrCursorSchema", err)
	}
}

// Through the domain: the port wired to this store reads a real
// state.vscdb end to end.
func TestExtractCursorThroughTheStore(t *testing.T) {
	root := t.TempDir()
	seed(t, filepath.Join(root, "globalStorage"), "cursorDiskKV", map[string]string{
		"bubbleId:c1:b1": `{"type":1,"text":"fix the retry path","createdAt":1756000000000}`,
		"bubbleId:c1:b2": `{"type":2,"text":"done","createdAt":1756000010000,"tokenCount":{"inputTokens":1200}}`,
	})
	agentdx.UseCursorStore(Store{})
	t.Cleanup(func() { agentdx.UseCursorStore(nil) })
	recs, err := agentdx.ExtractCursor(agentdx.ExtractOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].SessionID != "c1" {
		t.Errorf("records = %+v", recs)
	}
}
