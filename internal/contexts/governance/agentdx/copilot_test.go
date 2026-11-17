package agentdx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func copilotRecords(t *testing.T, since time.Time, withText bool, lines ...string) []Record {
	t.Helper()
	return readCopilotLog(strings.NewReader(strings.Join(lines, "\n")), "from-path", since, withText)
}

const copilotStart = `{"type":"session.start","id":"e0","timestamp":"2026-11-10T09:00:00Z","data":{"sessionId":"cp1","copilotVersion":"1.0.80","selectedModel":"claude-sonnet-4.5","reasoningEffort":"High"}}`

// Copilot CLI's typed events map one to one: the operator's message, the
// model's message, each tool call and its result, an abort and a
// compaction. A message the client wrote itself is not an instruction.
func TestCopilotRecords(t *testing.T) {
	got := copilotRecords(t, time.Time{}, false,
		copilotStart,
		`{"type":"user.message","id":"e1","timestamp":"2026-11-10T09:00:01Z","data":{"content":"fix the build"}}`,
		`{"type":"user.message","id":"e1b","timestamp":"2026-11-10T09:00:01Z","data":{"content":"use the skill","source":"skill-pdf"}}`,
		`{"type":"user.message","id":"e1c","timestamp":"2026-11-10T09:00:01Z","data":{"content":"keep going","isAutopilotContinuation":true}}`,
		`{"type":"assistant.message","id":"e2","timestamp":"2026-11-10T09:00:02Z","data":{"messageId":"m1","content":"on it","requestId":"r1","chunkIndex":0}}`,
		`{"type":"assistant.message","id":"e2b","timestamp":"2026-11-10T09:00:02Z","data":{"messageId":"m1","content":"more","requestId":"r1","chunkIndex":1}}`,
		`{"type":"tool.execution_start","id":"e3","timestamp":"2026-11-10T09:00:03Z","data":{"toolCallId":"c1","toolName":"bash","arguments":{"command":"git status"}}}`,
		`{"type":"tool.execution_start","id":"e3b","timestamp":"2026-11-10T09:00:03Z","data":{"toolCallId":"c0","toolName":"report_intent","arguments":{"intent":"Fixing"}}}`,
		`{"type":"tool.execution_complete","id":"e4","timestamp":"2026-11-10T09:00:04Z","data":{"toolCallId":"c1","success":true}}`,
		`{"type":"tool.execution_start","id":"e5","timestamp":"2026-11-10T09:00:05Z","data":{"toolCallId":"c2","toolName":"str_replace","arguments":{"path":"/w/main.go","old_str":"a","new_str":"b"}}}`,
		`{"type":"session.model_change","id":"e6","timestamp":"2026-11-10T09:00:06Z","data":{"newModel":"gpt-5"}}`,
		`{"type":"assistant.message","id":"e7","timestamp":"2026-11-10T09:00:07Z","data":{"messageId":"m2","content":"done"}}`,
		`{"type":"abort","id":"e8","timestamp":"2026-11-10T09:00:08Z","data":{"reason":"user_initiated"}}`,
		`{"type":"session.compaction_complete","id":"e9","timestamp":"2026-11-10T09:00:09Z","data":{"success":true}}`,
		`{"type":"assistant.usage","id":"e10","timestamp":"2026-11-10T09:00:10Z","ephemeral":true,"data":{"model":"gpt-5"}}`,
	)
	kinds := make([]string, 0, len(got))
	for _, r := range got {
		kinds = append(kinds, string(r.Kind))
		if r.SessionID != "cp1" {
			t.Errorf("session %q in %+v", r.SessionID, r)
		}
		if r.Text != "" {
			t.Errorf("text carried without being asked for: %+v", r)
		}
	}
	want := []string{"prompt", "assistant_turn", "tool_use", "tool_result", "tool_use", "assistant_turn", "interrupt", "compaction"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds %v\nwant  %v", kinds, want)
	}
	if got[1].Model != "claude-sonnet-4.5" || got[1].Effort != "high" || got[1].RequestID != "r1" {
		t.Errorf("first turn %+v", got[1])
	}
	if got[2].ToolName != "bash" || got[2].Command != "git status" || got[2].Mutates || got[2].CallID != "c1" || got[2].CallSignature == "" {
		t.Errorf("bash %+v", got[2])
	}
	if got[4].FilePath != "/w/main.go" || !got[4].Mutates || !isEdit(got[4].ToolName) {
		t.Errorf("edit %+v", got[4])
	}
	if got[5].Model != "gpt-5" || got[5].RequestID != "m2" {
		t.Errorf("turn after the model changed %+v", got[5])
	}
}

// The words are carried only when asked for, a rejection is recognised,
// a sub-agent's events are a session of their own, and the window drops
// what came before it while the model set before it still holds.
func TestCopilotTextSubagentsAndWindow(t *testing.T) {
	got := copilotRecords(t, time.Date(2026, 11, 10, 9, 0, 1, 0, time.UTC), true,
		copilotStart,
		`{"type":"user.message","id":"e1","timestamp":"2026-11-10T09:00:00Z","data":{"content":"too early"}}`,
		`{"type":"user.message","id":"e2","timestamp":"2026-11-10T09:00:02Z","data":{"content":"no, that's wrong"}}`,
		`{"type":"assistant.message","id":"e3","timestamp":"2026-11-10T09:00:03Z","agentId":"a7","data":{"messageId":"m1","content":"x"}}`,
	)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Text != "no, that's wrong" || !got[0].Rejects {
		t.Errorf("prompt %+v", got[0])
	}
	if got[1].SessionID != "cp1/a7" || got[1].Role != RoleSubagent || got[1].Model != "claude-sonnet-4.5" {
		t.Errorf("sub-agent turn %+v", got[1])
	}
}

// Early versions wrote no session id in the start record: the file names
// the session.
func TestCopilotSessionFromPath(t *testing.T) {
	got := copilotRecords(t, time.Time{}, false,
		`{"type":"session.start","id":"e0","timestamp":"2026-11-10T09:00:00Z","data":{"copilotVersion":"0.0.350"}}`,
		`{"type":"user.message","id":"e1","timestamp":"2026-11-10T09:00:01Z","data":{"content":"hi","attachments":[]}}`)
	if len(got) != 1 || got[0].SessionID != "from-path" {
		t.Errorf("got %+v", got)
	}
}

// ExtractAll reads Copilot CLI by name, from both layouts, stamped with
// its harness.
func TestExtractAllReadsCopilot(t *testing.T) {
	root := t.TempDir()
	write := func(path string, lines ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prompt := `{"type":"user.message","id":"e1","timestamp":"2026-11-10T09:00:01Z","data":{"content":"hi"}}`
	write(filepath.Join(root, "s-new", "events.jsonl"), prompt)
	write(filepath.Join(root, "s-old.jsonl"), prompt)
	got, err := ExtractAll(ExtractOptions{Source: SourceCopilot, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	sessions := map[string]bool{}
	for _, r := range got {
		if r.Harness != SourceCopilot || r.Kind != KindPrompt {
			t.Errorf("record %+v", r)
		}
		sessions[r.SessionID] = true
	}
	if len(got) != 2 || !sessions["s-new"] || !sessions["s-old"] {
		t.Errorf("got %+v", got)
	}
}
