package agentdx

import (
	"strings"
	"testing"
	"time"
)

func codexRecords(t *testing.T, lines ...string) []Record {
	t.Helper()
	return readCodexTranscript(strings.NewReader(strings.Join(lines, "\n")), "sess", time.Time{}, true)
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

// The defect this guards: current Codex emits no `user_message` event at
// all. The reader counted that event and skipped the user-role
// response_item to avoid double counting, so on real history it counted
// nothing — `tokenops dx --source codex` reported "0 instructions across
// 37 sessions" over 343 actual instructions.
//
// Zero-of-many is the hardest failure to notice, because an idle machine
// looks exactly the same.
func TestCodexCountsResponseItemPromptsWhenThereIsNoEvent(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser"}]}}`,
		`{"timestamp":"2026-08-23T10:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
	)
	var prompts []Record
	for _, r := range got {
		if r.Kind == KindPrompt {
			prompts = append(prompts, r)
		}
	}
	if len(prompts) != 1 {
		t.Fatalf("got %d prompts, want 1 — a rollout with no user_message event still has instructions in it", len(prompts))
	}
	if prompts[0].Text != "fix the parser" {
		t.Errorf("Text = %q, want the instruction as it was typed", prompts[0].Text)
	}
}

// Codex delivers plugin advertisements and permission preambles through
// the same user role a person types into. 56 of 399 user-role messages on
// real history were this; counting them inflates every per-instruction
// metric and makes the operator look chattier than they were.
func TestCodexSkipsInjectedUserMessages(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>\nhere is a list\n"}]}}`,
		`{"timestamp":"2026-08-23T10:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>"}]}}`,
		`{"timestamp":"2026-08-23T10:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"now fix the parser"}]}}`,
	)
	var prompts []Record
	for _, r := range got {
		if r.Kind == KindPrompt {
			prompts = append(prompts, r)
		}
	}
	if len(prompts) != 1 {
		t.Fatalf("got %d prompts, want 1 — only one of those was typed by a person", len(prompts))
	}
	if prompts[0].Text != "now fix the parser" {
		t.Errorf("Text = %q, want the human instruction", prompts[0].Text)
	}
}

// Prompts are held back until the channel is known, so the reader must
// restore file order before returning. Everything downstream — unit
// grouping, idle gaps, the story's boundaries — reads these in sequence.
func TestCodexReturnsRecordsInTimeOrder(t *testing.T) {
	got := codexRecords(t,
		`{"timestamp":"2026-08-23T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}}`,
		`{"timestamp":"2026-08-23T10:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
		`{"timestamp":"2026-08-23T10:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]}}`,
		`{"timestamp":"2026-08-23T10:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
	)
	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatalf("record %d (%s) precedes record %d (%s); order was not restored",
				i, got[i].At, i-1, got[i-1].At)
		}
	}
}

// Text is carried only when asked for, the same rule every reader
// follows: the words exist in memory for one scan and are never stored.
func TestCodexWithholdsTextUnlessAsked(t *testing.T) {
	line := `{"timestamp":"2026-08-23T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser"}]}}`
	got := readCodexTranscript(strings.NewReader(line), "sess", time.Time{}, false)
	for _, r := range got {
		if r.Kind == KindPrompt && r.Text != "" {
			t.Errorf("Text = %q with WithPromptText off", r.Text)
		}
	}
}
