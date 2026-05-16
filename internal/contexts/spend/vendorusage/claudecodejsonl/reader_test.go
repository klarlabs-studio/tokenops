package claudecodejsonl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReadFile must yield one Turn per assistant message with a usage
// block, skip user / tool / system turns, and tolerate malformed
// lines without aborting the scan.
func TestReadFileSkipsNonAssistantTurnsAndKeepsGoingOnMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jsonl")
	body := strings.Join([]string{
		// Garbage line — must not abort.
		`not even json`,
		// User turn — no usage, skipped.
		`{"type":"user","timestamp":"2026-05-14T09:22:40.000Z","sessionId":"s1","message":{"role":"user","content":"hi"}}`,
		// Assistant turn WITH usage — emitted.
		`{"type":"assistant","timestamp":"2026-05-14T09:22:45.151Z","sessionId":"s1","message":{"id":"msg_a","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50,"service_tier":"standard"}}}`,
		// Assistant turn with all-zero usage — skipped.
		`{"type":"assistant","timestamp":"2026-05-14T09:22:46.000Z","sessionId":"s1","message":{"id":"msg_b","model":"claude-opus-4-7","usage":{"input_tokens":0,"output_tokens":0}}}`,
		// Assistant turn with empty message ID — skipped.
		`{"type":"assistant","timestamp":"2026-05-14T09:22:47.000Z","sessionId":"s1","message":{"id":"","model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":2}}}`,
		// Assistant turn with bad timestamp — skipped.
		`{"type":"assistant","timestamp":"not-a-timestamp","sessionId":"s1","message":{"id":"msg_c","model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":2}}}`,
		// Tool turn — skipped (we only key on type=assistant).
		`{"type":"tool_use","timestamp":"2026-05-14T09:22:48.000Z","sessionId":"s1"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var turns []Turn
	err := ReadFile(path, func(t Turn) error {
		turns = append(turns, t)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected exactly 1 emitted turn; got %d", len(turns))
	}
	if turns[0].MessageID != "msg_a" {
		t.Errorf("MessageID = %q; want msg_a", turns[0].MessageID)
	}
	if turns[0].InputTokens != 10 || turns[0].OutputTokens != 20 {
		t.Errorf("token counts: in=%d out=%d", turns[0].InputTokens, turns[0].OutputTokens)
	}
	if turns[0].CacheReadInputTokens != 1000 || turns[0].CacheCreationInputTokens != 50 {
		t.Errorf("cache buckets: r=%d c=%d", turns[0].CacheReadInputTokens, turns[0].CacheCreationInputTokens)
	}
	if turns[0].ServiceTier != "standard" {
		t.Errorf("service_tier = %q", turns[0].ServiceTier)
	}
}

// FindSessionFiles must glob *.jsonl one level deep under root and
// return paths sorted lexicographically.
func TestFindSessionFiles(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"projA", "projB"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"s1.jsonl", "s2.jsonl"} {
			if err := os.WriteFile(filepath.Join(root, sub, n), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Decoy file at root + non-jsonl in a project.
	_ = os.WriteFile(filepath.Join(root, "stats-cache.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "projA", "notes.txt"), []byte(""), 0o644)
	got, err := FindSessionFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 jsonl files; got %d (%v)", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("paths not sorted: %v", got)
			break
		}
	}
}

// DefaultRoot must land under ~/.claude/projects.
func TestDefaultRoot(t *testing.T) {
	t.Setenv("HOME", filepath.FromSlash("/tmp/test-home"))
	p, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join(".claude", "projects")) {
		t.Errorf("DefaultRoot = %q; should end in .claude/projects", p)
	}
}
