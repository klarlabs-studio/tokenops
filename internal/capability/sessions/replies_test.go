package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplyFindingsReadsTheWindow(t *testing.T) {
	root := t.TempDir()
	lines := `{"type":"assistant","timestamp":"2026-01-05T10:00:00Z","sessionId":"s1","message":{"content":[{"type":"text","text":"done"}]}}
{"type":"assistant","timestamp":"2025-12-01T10:00:00Z","sessionId":"s1","message":{"content":[{"type":"text","text":"too old"}]}}
`
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := ReplyFindings(ReplyWindow{Root: root, Source: "claude-code", Since: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if f.TotalReplies != 1 {
		t.Errorf("replies %d, want the one in the window", f.TotalReplies)
	}
}
