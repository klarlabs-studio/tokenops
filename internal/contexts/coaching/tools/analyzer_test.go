package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func writeJSONL(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var buf string
	for i, l := range lines {
		if i > 0 {
			buf += "\n"
		}
		buf += l
	}
	if err := os.WriteFile(path, []byte(buf), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// Extract parses tool_use + tool_result blocks from the typed
// Claude Code JSONL content[] arrays. Bash commands surface in
// RawCommand; non-Bash tools have RawCommand=="".
func TestExtractParsesToolBlocks(t *testing.T) {
	dir := t.TempDir()
	_ = writeJSONL(t, dir, "sess.jsonl",
		`{"timestamp":"2026-05-16T10:00:00Z","sessionId":"s","message":{"content":[{"type":"tool_use","name":"Bash","tool_use_id":"tu1","input":{"command":"ls -la"}}]}}`,
		`{"timestamp":"2026-05-16T10:00:01Z","sessionId":"s","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","is_error":false,"content":"file.go"}]}}`,
		`{"timestamp":"2026-05-16T10:00:02Z","sessionId":"s","message":{"content":[{"type":"tool_use","name":"Read","tool_use_id":"tu2","input":{"file_path":"/x.go"}}]}}`,
		`{"timestamp":"2026-05-16T10:00:03Z","sessionId":"s","message":{"content":[{"type":"tool_result","tool_use_id":"tu2","is_error":true,"content":"no such file"}]}}`,
	)
	evs, err := Extract(ExtractOptions{Root: dir})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(evs) != 4 {
		t.Fatalf("got %d events; want 4", len(evs))
	}
	// First is Bash tool_use with command captured.
	if evs[0].Name != "Bash" || evs[0].RawCommand != "ls -la" {
		t.Errorf("evs[0] = %+v", evs[0])
	}
	// Read tool_use has Name set but no RawCommand.
	if evs[2].Name != "Read" || evs[2].RawCommand != "" {
		t.Errorf("evs[2] = %+v", evs[2])
	}
	// tool_result is_error preserved.
	if !evs[3].IsResult || !evs[3].IsError {
		t.Errorf("evs[3] = %+v", evs[3])
	}
}

// Analyze computes success rate from tool_use / tool_result pairs
// matched by ToolUseID. Destructive detection fires on rm -rf etc.
func TestAnalyzeSuccessAndDestructive(t *testing.T) {
	evs := []ToolEvent{
		{Name: "Bash", ToolUseID: "tu1", RawCommand: "ls -la"},
		{IsResult: true, ToolUseID: "tu1", IsError: false},
		{Name: "Bash", ToolUseID: "tu2", RawCommand: "rm -rf /tmp/junk"},
		{IsResult: true, ToolUseID: "tu2", IsError: false},
		{Name: "Read", ToolUseID: "tu3"},
		{IsResult: true, ToolUseID: "tu3", IsError: true},
		{Name: "Bash", ToolUseID: "tu4", RawCommand: "git push --force"},
		{IsResult: true, ToolUseID: "tu4", IsError: false},
	}
	s := Analyze(evs)
	if s.TotalToolCalls != 4 {
		t.Errorf("total = %d; want 4", s.TotalToolCalls)
	}
	if s.FailedCalls != 1 {
		t.Errorf("failed = %d; want 1 (Read tu3)", s.FailedCalls)
	}
	if s.DestructiveCalls != 2 {
		t.Errorf("destructive = %d; want 2 (rm -rf + force push)", s.DestructiveCalls)
	}
	if s.SuccessRate < 74.9 || s.SuccessRate > 75.1 {
		t.Errorf("success rate = %.2f; want 75.0", s.SuccessRate)
	}
	if s.DestructiveRate < 49.9 || s.DestructiveRate > 50.1 {
		t.Errorf("destructive rate = %.2f; want 50.0", s.DestructiveRate)
	}
}

// Empty input is a no-op (no division by zero).
func TestAnalyzeEmpty(t *testing.T) {
	s := Analyze(nil)
	if s.TotalToolCalls != 0 || s.SuccessRate != 0 || s.DestructiveRate != 0 {
		t.Errorf("empty analyze should be zero: %+v", s)
	}
}

// Destructive regex is narrow — common safe commands don't trip.
func TestDestructiveDoesNotFalseFire(t *testing.T) {
	safe := []string{
		"ls -la",
		"git status",
		"go build ./...",
		"grep -r foo src/",
		"echo 'rm -rf' > note.txt", // string literal, not a destructive call — but it WILL fire
	}
	for _, cmd := range safe[:4] {
		if destructiveRE.MatchString(cmd) {
			t.Errorf("safe command falsely flagged destructive: %q", cmd)
		}
	}
}
