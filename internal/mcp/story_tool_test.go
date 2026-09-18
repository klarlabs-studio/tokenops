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

func storyServer(t *testing.T, transcript string) *Server {
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
	if err := RegisterStoryTools(srv, StoryDeps{Root: root}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return srv
}

func decodeStory(t *testing.T, out string) storyResult {
	t.Helper()
	var res storyResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode (%q): %v", out, err)
	}
	return res
}

// The account existed only as JSON on stdout, which meant an agent could
// read its own history only by shelling out to its own telemetry. The
// facts are unchanged; asking for them is now a tool call.
func TestStoryToolReturnsTheAccount(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"s","message":{"content":"rewrite the boundary heuristic"}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:01Z","sessionId":"s","message":{"usage":{"input_tokens":100},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/a.go"}}]}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:02Z","sessionId":"s","message":{"usage":{"input_tokens":150},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/b.go"}}]}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:03Z","sessionId":"s","message":{"usage":{"input_tokens":200},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/a.go"}}]}}`,
	}
	res := decodeStory(t, execTool(t, storyServer(t, strings.Join(lines, "\n")), "tokenops_story",
		map[string]any{"days": 0}))

	if len(res.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1: %+v", len(res.Tasks), res.Tasks)
	}
	task := res.Tasks[0]
	// Quoted, not summarised: a summariser can be wrong and a quote cannot.
	if task.Title != "rewrite the boundary heuristic" {
		t.Errorf("Title = %q, want the instruction as it was typed", task.Title)
	}
	if task.Instructions != 1 {
		t.Errorf("Instructions = %d, want 1", task.Instructions)
	}
	if task.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", task.ToolCalls)
	}
	// Returning to /a.go after moving on to /b.go is rework, and seeing it
	// is the point: an agent that knows it has already been here can stop
	// instead of pressing on.
	//
	// The fixture used to be two consecutive edits to one file, which is
	// how a multi-part change is made rather than friction — the premise
	// the metric itself was corrected on.
	if task.Clean {
		t.Error("Clean = true, want the repeated edit reported as friction")
	}
	if len(task.Frictions) == 0 {
		t.Error("Frictions is empty; the repeated edit should be enumerated")
	}
}

// An empty window says so rather than returning a bare empty list. An
// agent reading zero tasks needs to know whether that means "nothing
// happened" or "you asked the wrong question".
func TestStoryToolExplainsAnEmptyWindow(t *testing.T) {
	res := decodeStory(t, execTool(t, storyServer(t, ""), "tokenops_story", map[string]any{"days": 7}))
	if len(res.Tasks) != 0 {
		t.Fatalf("got %d tasks from an empty transcript", len(res.Tasks))
	}
	if res.Note == "" {
		t.Error("no note explaining the empty result")
	}
}

// Newest first: the work being asked about is the work just done.
func TestStoryToolReturnsNewestFirst(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"a","message":{"content":"the older piece of work"}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:01Z","sessionId":"a","message":{"usage":{"input_tokens":100}}}`,
		`{"type":"user","timestamp":"2099-01-02T10:00:00Z","sessionId":"b","message":{"content":"the newer piece of work"}}`,
		`{"type":"assistant","timestamp":"2099-01-02T10:00:01Z","sessionId":"b","message":{"usage":{"input_tokens":100}}}`,
	}
	res := decodeStory(t, execTool(t, storyServer(t, strings.Join(lines, "\n")), "tokenops_story",
		map[string]any{"days": 0}))
	if len(res.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(res.Tasks))
	}
	if res.Tasks[0].Title != "the newer piece of work" {
		t.Errorf("first task = %q, want the most recent", res.Tasks[0].Title)
	}
}
