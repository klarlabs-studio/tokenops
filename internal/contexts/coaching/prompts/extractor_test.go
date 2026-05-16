package prompts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeJSONL writes the given lines to a temp .jsonl file and returns
// the full path. One line per call argument; no trailing newline.
func writeJSONL(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(joinLines(lines)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func joinLines(lines []string) string {
	var out string
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

// Extract recognises both the legacy string content shape and the
// current typed-array shape, filtering out tool-result + synthetic
// system messages.
func TestExtractBothContentShapes(t *testing.T) {
	dir := t.TempDir()
	_ = writeJSONL(t, dir, "sess-abc.jsonl",
		`{"type":"user","sessionId":"s1","timestamp":"2026-05-16T10:00:00Z","message":{"content":"refactor auth middleware"}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-05-16T10:01:00Z","message":{"content":[{"type":"text","text":"add tests for retries"}]}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-05-16T10:02:00Z","message":{"content":[{"type":"text","text":"sure"}]}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-05-16T10:03:00Z","message":{"content":"<system-reminder>noise</system-reminder>"}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-05-16T10:04:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1"}]}}`,
	)
	got, err := Extract(ExtractOptions{Root: dir})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d prompts; want 2 (real user turns only)", len(got))
	}
	if got[0].Text != "refactor auth middleware" {
		t.Errorf("first text = %q", got[0].Text)
	}
	if got[1].Text != "add tests for retries" {
		t.Errorf("second text = %q", got[1].Text)
	}
}

// Since/Until filters drop turns outside the window.
func TestExtractTimeWindowFilters(t *testing.T) {
	dir := t.TempDir()
	_ = writeJSONL(t, dir, "sess.jsonl",
		`{"type":"user","sessionId":"s","timestamp":"2026-05-15T10:00:00Z","message":{"content":"old"}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-05-16T10:00:00Z","message":{"content":"in window"}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-05-17T10:00:00Z","message":{"content":"future"}}`,
	)
	got, err := Extract(ExtractOptions{
		Root:  dir,
		Since: time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 1 || got[0].Text != "in window" {
		t.Errorf("filtered = %+v", got)
	}
}

// SessionID restricts the walk to one filename stem.
func TestExtractSessionFilter(t *testing.T) {
	dir := t.TempDir()
	_ = writeJSONL(t, dir, "sess-a.jsonl",
		`{"type":"user","sessionId":"sess-a","timestamp":"2026-05-16T10:00:00Z","message":{"content":"from a"}}`,
	)
	_ = writeJSONL(t, dir, "sess-b.jsonl",
		`{"type":"user","sessionId":"sess-b","timestamp":"2026-05-16T10:00:00Z","message":{"content":"from b"}}`,
	)
	got, err := Extract(ExtractOptions{Root: dir, SessionID: "sess-a"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 1 || got[0].Text != "from a" {
		t.Errorf("filtered = %+v", got)
	}
}

// Limit caps the result at the first N matches and stops walking.
func TestExtractLimit(t *testing.T) {
	dir := t.TempDir()
	_ = writeJSONL(t, dir, "sess.jsonl",
		`{"type":"user","sessionId":"s","timestamp":"2026-05-16T10:00:00Z","message":{"content":"one"}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-05-16T10:01:00Z","message":{"content":"two"}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-05-16T10:02:00Z","message":{"content":"three"}}`,
	)
	got, err := Extract(ExtractOptions{Root: dir, Limit: 2})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("limit=2 produced %d", len(got))
	}
}

// Malformed lines are skipped, not fatal.
func TestExtractToleratesMalformedLines(t *testing.T) {
	dir := t.TempDir()
	_ = writeJSONL(t, dir, "sess.jsonl",
		`{"type":"user","sessionId":"s","timestamp":"2026-05-16T10:00:00Z","message":{"content":"good"}}`,
		`this is not json`,
		`{"type":"user"}`,
		`{"type":"user","sessionId":"s","timestamp":"not-a-timestamp","message":{"content":"bad ts"}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-05-16T10:01:00Z","message":{"content":"also good"}}`,
	)
	got, err := Extract(ExtractOptions{Root: dir})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("malformed-tolerant extract produced %d", len(got))
	}
}
