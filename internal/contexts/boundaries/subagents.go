package boundaries

import (
	"fmt"
	"strconv"
)

// The fan-out figures: how many subagents a session has started, counting
// the one about to start, in the whole session and since the operator's
// last prompt. A prompt that sends twenty agents at once spends a plan
// window as surely as a long session does, and a person notices it later.

const (
	// FieldSessionSubagents is the subagents started in the session,
	// counting the one about to start.
	FieldSessionSubagents Field = "session_subagents"
	// FieldPromptSubagents is the subagents started since the operator's
	// last prompt, counting the one about to start.
	FieldPromptSubagents Field = "prompt_subagents"
)

// maxSubagents bounds a subagent threshold: far past any fan-out a session
// can run.
const maxSubagents = 1000

// SubagentFigure is the subagents a session started, when the pause point
// could read them. Prompt is unknown while no prompt is found.
type SubagentFigure struct {
	Known       bool
	Session     int
	Prompt      int
	PromptKnown bool
}

// parseSubagents reads a subagent-count threshold: a whole number.
func parseSubagents(value string) (float64, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number", value)
	}
	if n < 0 || n > maxSubagents {
		return 0, fmt.Errorf("%d must be between 0 and %d", n, maxSubagents)
	}
	return float64(n), nil
}

// subagentCount is the figure a fan-out condition reads.
func subagentCount(f SubagentFigure, field Field) (int, bool) {
	if !f.Known {
		return 0, false
	}
	if field == FieldPromptSubagents {
		return f.Prompt, f.PromptKnown
	}
	return f.Session, true
}

// subagentWords says what was counted: "subagent 6 since the last prompt
// (> 5)".
func subagentWords(n int, field Field, c Condition) string {
	scope := "in this session"
	if field == FieldPromptSubagents {
		scope = "since the last prompt"
	}
	return fmt.Sprintf("subagent %d %s (%s %g)", n, scope, c.Op, c.Number)
}
