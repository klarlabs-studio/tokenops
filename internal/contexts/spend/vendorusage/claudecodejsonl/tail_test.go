package claudecodejsonl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ReadTail reads only the end of a transcript: the turns there, one per
// message (the first line of each, timed from what preceded it), and when
// the last entry was written.
func TestReadTail(t *testing.T) {
	filler := `{"type":"user","timestamp":"2026-08-23T09:00:00Z","sessionId":"s","message":{"content":"` + strings.Repeat("x", 4096) + `"}}`
	lines := []string{
		filler,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:20Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"cache_read_input_tokens":900,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":100}}}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:25Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"cache_read_input_tokens":900,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":100}}}}`,
		`{"type":"user","timestamp":"2026-08-23T10:01:00Z","sessionId":"s","message":{"content":[{"type":"tool_result"}]}}`,
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turns, last, err := ReadTail(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].MessageID != "a1" || turns[0].Latency != 20*time.Second || turns[0].CacheCreation1hInputTokens != 100 {
		t.Fatalf("turns = %+v", turns)
	}
	if want := time.Date(2026, 8, 23, 10, 1, 0, 0, time.UTC); !last.Equal(want) {
		t.Errorf("last entry = %v, want %v", last, want)
	}
	if _, _, err := ReadTail(filepath.Join(t.TempDir(), "missing.jsonl"), 1024); err == nil {
		t.Error("no error for a missing transcript")
	}
}
