package agentdx

import (
	"strings"
	"testing"
	"time"
)

func recordsFrom(t *testing.T, lines ...string) []Record {
	t.Helper()
	return readTranscript(strings.NewReader(strings.Join(lines, "\n")), time.Time{})
}

// A typed instruction opens a unit; the tool results the client writes
// back under the same "user" type do not. They outnumber real prompts by
// roughly 44 to 1, so conflating them would inflate every per-prompt
// metric beyond usefulness.
func TestExtractDistinguishesPromptsFromToolEchoes(t *testing.T) {
	got := recordsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"fix the bug"}}`,
		`{"type":"user","timestamp":"2026-08-23T10:00:05Z","sessionId":"s","message":{"content":[{"type":"tool_result"}]}}`,
	)
	var prompts int
	for _, r := range got {
		if r.Kind == KindPrompt {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("prompts = %d, want 1: %+v", prompts, got)
	}
}

// Tool calls are pulled out of the assistant turn that made them, with
// the file they touched, so rework can be attributed.
func TestExtractPullsToolCallsWithPaths(t *testing.T) {
	got := recordsFrom(t,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","message":{"usage":{"input_tokens":100},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/a.go"}}]}}`,
	)
	var sawTurn, sawTool bool
	for _, r := range got {
		if r.Kind == KindAssistantTurn && r.InputTokens == 100 {
			sawTurn = true
		}
		if r.Kind == KindToolUse && r.ToolName == "Edit" && r.FilePath == "/a.go" {
			sawTool = true
		}
	}
	if !sawTurn || !sawTool {
		t.Errorf("expected both a turn and its tool call: %+v", got)
	}
}

// An interrupt is the operator stopping the agent — the truest DX signal
// available, and it must not be mistaken for a new instruction.
func TestExtractRecognisesInterrupt(t *testing.T) {
	got := recordsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"[Request interrupted by user]"}}`,
	)
	if len(got) != 1 || got[0].Kind != KindInterrupt {
		t.Errorf("got %+v, want a single interrupt record", got)
	}
}

// Context size counts cache reads and writes, because that is what the
// window actually holds.
func TestExtractCountsFullContext(t *testing.T) {
	got := recordsFrom(t,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:01Z","sessionId":"s","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":500,"cache_creation_input_tokens":40}}}`,
	)
	if len(got) != 1 || got[0].InputTokens != 550 {
		t.Errorf("InputTokens = %+v, want 550", got)
	}
}

// A half-written final line is normal in a live transcript.
func TestExtractSkipsMalformedLines(t *testing.T) {
	got := recordsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-`,
	)
	if len(got) != 1 {
		t.Errorf("got %+v, want just the well-formed line", got)
	}
}

// Since filters by time so a window can be scoped.
func TestExtractHonoursSince(t *testing.T) {
	lines := `{"type":"user","timestamp":"2026-08-01T10:00:00Z","sessionId":"s","message":{"content":"old"}}
{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"new"}}`
	got := readTranscript(strings.NewReader(lines), time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 {
		t.Errorf("got %+v, want only the entry inside the window", got)
	}
}

// The client marks a compaction on the USER entry that carries the
// summary, not on an assistant turn. Checking the wrong branch reported
// zero compactions on a corpus that had 21 in the window — a metric
// silently reading zero is worse than one that is absent.
func TestExtractRecognisesCompaction(t *testing.T) {
	got := recordsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","isCompactSummary":true,"message":{"content":"summary of prior context"}}`,
	)
	if len(got) != 1 || got[0].Kind != KindCompaction {
		t.Errorf("got %+v, want a single compaction record", got)
	}
}

// A compaction summary must not also count as an instruction the
// operator typed, or it inflates the per-prompt denominator.
func TestCompactionIsNotAPrompt(t *testing.T) {
	got := recordsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","isCompactSummary":true,"message":{"content":"summary"}}`,
		`{"type":"user","timestamp":"2026-08-23T10:00:10Z","sessionId":"s","message":{"content":"now continue"}}`,
	)
	var prompts int
	for _, r := range got {
		if r.Kind == KindPrompt {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("prompts = %d, want 1 — a compaction summary is not an instruction", prompts)
	}
}
