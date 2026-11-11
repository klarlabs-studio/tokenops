package agentdx

import (
	"math"
	"testing"
	"time"
)

func at(sec int) time.Time { return time.Date(2026, 8, 23, 10, 0, sec, 0, time.UTC) }

func prompt(s int, sess string) Record {
	return Record{At: at(s), SessionID: sess, Kind: KindPrompt}
}
func turn(s int, sess string, input int64) Record {
	return Record{At: at(s), SessionID: sess, Kind: KindAssistantTurn, InputTokens: input}
}
func tool(s int, sess, name, path string) Record {
	return Record{At: at(s), SessionID: sess, Kind: KindToolUse, ToolName: name, FilePath: path}
}

// Turns-per-prompt is the closest thing to "did the agent finish
// efficiently". CGR already says something is costing turns; this says
// how many, without requiring the operator to mark tasks by hand.
func TestTurnsPerPrompt(t *testing.T) {
	m := Compute([]Record{
		prompt(0, "s"), turn(1, "s", 0), turn(2, "s", 0), turn(3, "s", 0),
		prompt(10, "s"), turn(11, "s", 0),
	})
	if m.Prompts != 2 {
		t.Fatalf("Prompts = %d, want 2", m.Prompts)
	}
	if m.MedianTurnsPerPrompt != 2 {
		t.Errorf("MedianTurnsPerPrompt = %v, want 2 (median of 3 and 1)", m.MedianTurnsPerPrompt)
	}
}

// Editing a file that was already edited answering the same prompt is
// rework — the agent did not get it right the first time.
func TestReworkRate(t *testing.T) {
	m := Compute([]Record{
		prompt(0, "s"),
		tool(1, "s", "Edit", "/a.go"),
		tool(2, "s", "Edit", "/b.go"),
		tool(3, "s", "Edit", "/a.go"), // rework
	})
	if math.Abs(m.ReworkRatePct-33.3) > 0.5 {
		t.Errorf("ReworkRatePct = %v, want ~33.3 (1 of 3 edits)", m.ReworkRatePct)
	}
}

// A file edited under a different prompt is not rework — it is the next
// piece of work, and counting it would make normal iteration look like
// failure.
func TestReworkResetsBetweenPrompts(t *testing.T) {
	m := Compute([]Record{
		prompt(0, "s"), tool(1, "s", "Edit", "/a.go"),
		prompt(10, "s"), tool(11, "s", "Edit", "/a.go"),
	})
	if m.ReworkRatePct != 0 {
		t.Errorf("ReworkRatePct = %v, want 0 across separate prompts", m.ReworkRatePct)
	}
}

// Delegating to a subagent is a real DX signal: the main agent decided
// the work needed its own context.
func TestEscalationRate(t *testing.T) {
	m := Compute([]Record{
		prompt(0, "s"), tool(1, "s", "Task", ""),
		prompt(10, "s"), tool(11, "s", "Edit", "/a.go"),
	})
	if m.EscalationRatePct != 50 {
		t.Errorf("EscalationRatePct = %v, want 50 (1 of 2 prompts delegated)", m.EscalationRatePct)
	}
}

// Context growth per turn is what eventually forces a compaction.
func TestContextGrowth(t *testing.T) {
	m := Compute([]Record{
		prompt(0, "s"), turn(1, "s", 1000), turn(2, "s", 3000), turn(3, "s", 5000),
	})
	if m.MedianContextGrowthTokens != 2000 {
		t.Errorf("MedianContextGrowthTokens = %d, want 2000", m.MedianContextGrowthTokens)
	}
}

// Every compaction is a context-management failure worth counting.
func TestCompactionsPerSession(t *testing.T) {
	m := Compute([]Record{
		{At: at(0), SessionID: "a", Kind: KindPrompt},
		{At: at(1), SessionID: "a", Kind: KindCompaction},
		{At: at(2), SessionID: "a", Kind: KindCompaction},
		{At: at(3), SessionID: "b", Kind: KindPrompt},
	})
	if m.CompactionsPerSession != 1 {
		t.Errorf("CompactionsPerSession = %v, want 1 (2 across 2 sessions)", m.CompactionsPerSession)
	}
}

// An interrupt is the operator saying the agent went the wrong way —
// arguably the truest DX signal available.
func TestInterruptRate(t *testing.T) {
	m := Compute([]Record{
		prompt(0, "s"), turn(1, "s", 0),
		{At: at(2), SessionID: "s", Kind: KindInterrupt},
		prompt(10, "s"), turn(11, "s", 0),
	})
	if m.InterruptRatePct != 50 {
		t.Errorf("InterruptRatePct = %v, want 50 (1 of 2 prompts interrupted)", m.InterruptRatePct)
	}
}

// Nothing in, nothing claimed.
func TestEmptyInput(t *testing.T) {
	m := Compute(nil)
	if m.Prompts != 0 || m.MedianTurnsPerPrompt != 0 {
		t.Errorf("empty input should yield zero metrics: %+v", m)
	}
}

// Turns before the first prompt belong to no prompt unit and must not
// invent one.
func TestTurnsBeforeFirstPromptIgnored(t *testing.T) {
	m := Compute([]Record{
		turn(0, "s", 0), turn(1, "s", 0),
		prompt(5, "s"), turn(6, "s", 0),
	})
	if m.Prompts != 1 {
		t.Fatalf("Prompts = %d, want 1", m.Prompts)
	}
	if m.MedianTurnsPerPrompt != 1 {
		t.Errorf("MedianTurnsPerPrompt = %v, want 1 — orphan turns must not be attributed", m.MedianTurnsPerPrompt)
	}
}

// A client that never records an interrupt or a compaction has no rate of
// either: its instructions and sessions are left out of the denominator,
// and a window of only such a client is not graded on them. Counted in,
// its missing records read as clean runs and graded A.
func TestUnrecordableKindsAreNotRated(t *testing.T) {
	from := func(h Source, rs ...Record) []Record {
		for i := range rs {
			rs[i].Harness = h
		}
		return rs
	}
	claude := from(SourceClaudeCode,
		prompt(0, "c"), turn(1, "c", 0),
		Record{At: at(2), SessionID: "c", Kind: KindInterrupt},
		Record{At: at(3), SessionID: "c", Kind: KindCompaction},
		prompt(10, "c"), turn(11, "c", 0))
	cursor := from(SourceCursor, prompt(20, "u"), turn(21, "u", 0), prompt(30, "u"), turn(31, "u", 0))
	codex := from(SourceCodex, prompt(40, "x"), turn(41, "x", 0))
	opencode := from(SourceOpencode, prompt(50, "o"), turn(51, "o", 0))

	m := Compute(append(append(append(append([]Record{}, claude...), cursor...), codex...), opencode...))
	// One interrupt in Claude Code's two instructions and Codex's one;
	// Cursor's and opencode's three are not counted.
	if m.InterruptPrompts != 3 || m.InterruptRatePct != 33.3 {
		t.Errorf("interrupts over %d instructions at %v%%; want 3 and 33.3", m.InterruptPrompts, m.InterruptRatePct)
	}
	// One compaction over Claude Code's and opencode's sessions.
	if m.CompactionSessions != 2 || m.CompactionsPerSession != 0.5 {
		t.Errorf("compactions over %d sessions at %v; want 2 and 0.5", m.CompactionSessions, m.CompactionsPerSession)
	}

	only := Compute(cursor)
	if only.InterruptPrompts != 0 || only.CompactionSessions != 0 {
		t.Errorf("cursor alone: %d instructions, %d sessions rated", only.InterruptPrompts, only.CompactionSessions)
	}
	if g := Grade(only); g.Interrupt != "" || g.Compaction != "" {
		t.Errorf("cursor alone graded interrupt %q, compaction %q on records it cannot write", g.Interrupt, g.Compaction)
	}
}
