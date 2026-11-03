package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// TestPromptFanOut stops the sixth subagent one prompt sends, counted from
// the session's transcript, and lets the first of the next prompt go.
func TestPromptFanOut(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "fan-out", Action: "subagent_model", Require: "ask", Stage: "enforce",
		When: map[string]string{"prompt_subagents": "> 5"}})
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	ids := []string{"a1", "a2", "a3", "a4", "a5"}
	lines := make([]string, 0, 3)
	lines = append(lines, `{"type":"user","message":{"role":"user","content":"map every package"}}`)
	uses := make([]string, 0, len(ids))
	for _, id := range ids {
		uses = append(uses, `{"type":"tool_use","id":"`+id+`","name":"Agent","input":{}}`)
	}
	lines = append(lines, `{"type":"assistant","message":{"content":[`+strings.Join(uses, ",")+`]}}`)
	write := func() {
		t.Helper()
		if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	c := call("s1")
	c.Action, c.TranscriptPath, c.ToolUseID = domain.ActionSubagentModel, transcript, "a6"
	res, err := Guard(r, c, env)
	if err != nil || res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "subagent 6 since the last prompt (> 5)") {
		t.Fatalf("sixth subagent: %+v, %v", res, err)
	}
	// One already in the transcript is the one asked about, not a sixth.
	c.ToolUseID = "a5"
	if res, _ := Guard(r, c, env); res.Verdict != VerdictNone {
		t.Fatalf("fifth subagent counted twice: %+v", res)
	}
	lines = append(lines, `{"type":"user","message":{"role":"user","content":"thanks, now the docs"}}`)
	write()
	c.ToolUseID = "b1"
	if res, _ := Guard(r, c, env); res.Verdict != VerdictNone {
		t.Fatalf("a new prompt did not start the count again: %+v", res)
	}
}
