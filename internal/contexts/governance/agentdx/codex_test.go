package agentdx

import (
	"strings"
	"testing"
	"time"
)

func codexRecords(t *testing.T, lines ...string) []Record {
	t.Helper()
	return readCodexTranscript(strings.NewReader(strings.Join(lines, "\n")), "sess", time.Time{})
}

// Codex marks the operator's instruction explicitly, which is better
// evidence than Claude Code's content-shape heuristic — there is no
// guessing about which "user" rows are really tool results.
func TestCodexPromptFromUserMessageEvent(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"event_msg","payload":{"type":"user_message"}}`,
	)
	if len(got) != 1 || got[0].Kind != KindPrompt {
		t.Errorf("got %+v, want a single prompt", got)
	}
}

// turn_aborted is Codex saying the operator stopped it — an explicit
// interrupt signal, where Claude Code only leaves a text marker.
func TestCodexInterruptFromTurnAborted(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"event_msg","payload":{"type":"turn_aborted"}}`,
	)
	if len(got) != 1 || got[0].Kind != KindInterrupt {
		t.Errorf("got %+v, want an interrupt", got)
	}
}

// A function_call is a tool use; its name is the tool.
func TestCodexToolFromFunctionCall(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
	)
	if len(got) != 1 || got[0].Kind != KindToolUse || got[0].ToolName != "exec_command" {
		t.Errorf("got %+v, want an exec_command tool use", got)
	}
}

// An assistant message is a turn, and token_count carries the context
// size that turn ran with.
func TestCodexTurnAndContextSize(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":17821,"cached_input_tokens":5504}}}}`,
		`{"timestamp":"2026-08-23T10:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
	)
	var turn *Record
	for i := range got {
		if got[i].Kind == KindAssistantTurn {
			turn = &got[i]
		}
	}
	if turn == nil {
		t.Fatalf("no assistant turn in %+v", got)
	}
	if turn.InputTokens != 23325 {
		t.Errorf("InputTokens = %d, want 23325 (input + cached)", turn.InputTokens)
	}
}

// A developer-role message is injected scaffolding, not something the
// operator typed. Counting it would inflate every per-instruction metric.
func TestCodexIgnoresDeveloperMessages(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions>"}]}}`,
	)
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing for injected scaffolding", got)
	}
}

// A user-role response_item duplicates the user_message event; counting
// both would double every instruction.
func TestCodexDoesNotDoubleCountPrompts(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"do it"}]}}`,
		`{"timestamp":"2026-08-23T10:00:01Z","type":"event_msg","payload":{"type":"user_message"}}`,
	)
	var prompts int
	for _, r := range got {
		if r.Kind == KindPrompt {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("prompts = %d, want 1 — the event and the item are the same instruction", prompts)
	}
}
