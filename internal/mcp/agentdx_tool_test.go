package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dxServer(t *testing.T, transcript string) *Server {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterAgentDXTools(srv, AgentDXDeps{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return srv
}

// The agent has to be able to see the session it is in. As a CLI report
// these metrics were seen only when the operator remembered to look.
func TestAgentDXToolReportsGradedMetrics(t *testing.T) {
	// One instruction that drags: many turns, and a file edited twice.
	lines := make([]string, 0, 31)
	lines = append(lines,
		`{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"s","message":{"content":"do the thing"}}`)
	for i := range 30 {
		lines = append(lines,
			`{"type":"assistant","timestamp":"2099-01-01T10:00:0`+string(rune('0'+i%10))+`Z","sessionId":"s","message":{"usage":{"input_tokens":100},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/a.go"}}]}}`)
	}
	out := execTool(t, dxServer(t, strings.Join(lines, "\n")), "tokenops_agent_dx",
		map[string]any{"days": 0})

	var res agentDXResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode (%q): %v", out, err)
	}
	if res.Metrics.Prompts != 1 {
		t.Errorf("Prompts = %d, want 1", res.Metrics.Prompts)
	}
	if res.Grades.Turns == "" {
		t.Error("turns must be graded, not just reported")
	}
	if res.Grades.Overall == "" {
		t.Error("an overall grade is what makes this actionable at a glance")
	}
	if res.Recommendation == nil {
		t.Fatal("a poorly-graded profile must name the change to make")
	}
	if res.Recommendation.Action == "" || res.Recommendation.Evidence == "" {
		t.Errorf("a recommendation needs evidence and an action: %+v", res.Recommendation)
	}
}

// An empty window says so plainly instead of returning zeros that look
// like a perfect score.
func TestAgentDXToolEmptyWindow(t *testing.T) {
	out := execTool(t, dxServer(t, ""), "tokenops_agent_dx", map[string]any{"days": 7})
	var res agentDXResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Grades.Overall != "" {
		t.Errorf("no data must yield no grade, got %q", res.Grades.Overall)
	}
	if !strings.Contains(res.Note, "no instructions") {
		t.Errorf("Note = %q, want an explanation", res.Note)
	}
}
