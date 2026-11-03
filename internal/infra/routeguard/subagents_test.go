package routeguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func promptLine(text string) string {
	return `{"type":"user","timestamp":"2026-11-03T10:00:00Z","message":{"role":"user","content":"` + text + `"}}`
}

func agentLine(ids ...string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, `{"type":"tool_use","id":"`+id+`","name":"Agent","input":{"description":"look"}}`)
	}
	return `{"type":"assistant","timestamp":"2026-11-03T10:00:01Z","message":{"model":"claude-opus-4-1","content":[` + strings.Join(parts, ",") + `]}}`
}

func resultLine(id string, isError bool, text string) string {
	e := "false"
	if isError {
		e = "true"
	}
	return `{"type":"user","timestamp":"2026-11-03T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` +
		id + `","is_error":` + e + `,"content":"` + text + `"}]}}`
}

func TestSubagentLaunches(t *testing.T) {
	path := writeTranscript(t,
		promptLine("map the repo"),
		agentLine("a1", "a2"),
		resultLine("a1", false, "done"),
		resultLine("a2", true, "The user doesn't want to proceed with this tool use. The tool use was rejected"),
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"tool_use","id":"side","name":"Task","input":{}}]}}`,
		promptLine("<command-name>/clear</command-name>"),
		promptLine("now fix it"),
		agentLine("b1"),
		resultLine("b1", true, `tokenops boundary \"fan-out\": subagent 2 since the last prompt`),
		agentLine("b2", "b3", "now"),
	)
	got, ok := SubagentLaunches(path, "now")
	if !ok {
		t.Fatal("transcript not read")
	}
	// a1 ran; a2 was declined, b1 refused, the sidechain call is a
	// subagent's own; b2 and b3 have no result yet; now is about to start.
	want := Launches{Session: 3, Prompt: 2, PromptFound: true}
	if got != want {
		t.Fatalf("SubagentLaunches = %+v, want %+v", got, want)
	}
}

func TestSubagentLaunchesWithoutPrompt(t *testing.T) {
	got, ok := SubagentLaunches(writeTranscript(t, agentLine("a1")), "")
	if !ok || got != (Launches{Session: 1}) {
		t.Fatalf("SubagentLaunches = %+v %v, want one launch and no prompt", got, ok)
	}
	if _, ok := SubagentLaunches(filepath.Join(t.TempDir(), "missing.jsonl"), ""); ok {
		t.Fatal("a missing transcript read as known")
	}
}
