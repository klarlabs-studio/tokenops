package boundaries

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The loop breaker's figure: how many of the session's latest tool calls
// went round a loop with no file changed.
//
// Repetition alone is a poor loop detector: an agent reads the same file
// twice for good reasons. Repetition with no progress in between is a much
// better one (IBM, "Unsupervised Cycle Detection in Agentic Applications",
// arXiv 2511.10650). The pause point sees each tool's name and input, but
// not whether a call succeeded, so progress is an edit or a write it has
// not seen before. A file changed through a shell command is not seen.

// FieldLoopCalls is the number of the session's latest calls that went
// round a loop: the same call again and again, or the same cycle of two or
// three calls at least three times, with no file changed in between.
const FieldLoopCalls Field = "loop_calls"

// LoopWindow is how many of a session's latest calls the loop figure
// reads. A loop longer than that reads as LoopWindow calls.
const LoopWindow = 64

// loopMinCycleRepeats is how often a cycle of two or three calls must go
// round before it counts: twice is too often ordinary work (test, read,
// test, read).
const loopMinCycleRepeats = 3

// loopMaxPeriod is the longest cycle looked for.
const loopMaxPeriod = 3

// ToolCall is one tool call as the loop figure keeps it: the tool's name
// and a hash of its input, never the input.
type ToolCall struct {
	Tool string `json:"t"`
	Hash string `json:"h"`
}

// LoopReading is the loop at the end of a session's calls, if any.
type LoopReading struct {
	// Calls is how many of the latest calls are in the loop: Period times
	// Repeats. Zero is no loop.
	Calls int
	// Period is the length of the repeating unit: 1 for the same call
	// again, 2 or 3 for a cycle. Repeats is how often it went round.
	Period  int
	Repeats int
	// Tools are the unit's tool names, in the order they ran.
	Tools []string
}

// LoopFigure is the loop reading, when the pause point could read one.
type LoopFigure struct {
	Known   bool
	Reading LoopReading
}

// editTools are the tools whose call changes a file, in Claude Code and
// opencode.
var editTools = []string{"edit", "multiedit", "write", "notebookedit", "patch"}

// IsEditTool reports whether a tool changes a file.
func IsEditTool(name string) bool {
	return slices.Contains(editTools, strings.ToLower(strings.TrimSpace(name)))
}

// ReadLoop reads the loop at the end of calls, oldest first. Only the calls
// since the last progress count: an edit or a write not already made since
// the one before it. Repeating the same edit is no progress (it fails, or
// changes nothing), so it stays in the window it would otherwise reset.
func ReadLoop(calls []ToolCall) LoopReading {
	tail := sinceProgress(calls)
	best := LoopReading{}
	for period := 1; period <= loopMaxPeriod && period <= len(tail); period++ {
		unit := tail[len(tail)-period:]
		if period > 1 && allSame(unit) {
			continue
		}
		repeats := 1
		for end := len(tail) - period; end-period >= 0 && slices.Equal(tail[end-period:end], unit); end -= period {
			repeats++
		}
		if repeats < 2 || (period > 1 && repeats < loopMinCycleRepeats) {
			continue
		}
		if n := period * repeats; n > best.Calls {
			best = LoopReading{Calls: min(n, LoopWindow), Period: period, Repeats: repeats, Tools: toolNames(unit)}
		}
	}
	return best
}

// sinceProgress is the calls after the last progress, starting with the
// edit that made it, so a repeat of that same edit is seen as one.
func sinceProgress(calls []ToolCall) []ToolCall {
	start := 0
	for i, c := range calls {
		if IsEditTool(c.Tool) && !slices.Contains(calls[start:i], c) {
			start = i
		}
	}
	return calls[start:]
}

func allSame(unit []ToolCall) bool {
	for _, c := range unit[1:] {
		if c != unit[0] {
			return false
		}
	}
	return true
}

func toolNames(unit []ToolCall) []string {
	out := make([]string, len(unit))
	for i, c := range unit {
		out[i] = c.Tool
	}
	return out
}

// loopWords says what repeated, in words a person and an agent read:
// "the same Bash call 4 times in a row with no file changed".
func loopWords(r LoopReading) string {
	if r.Period == 1 {
		return fmt.Sprintf("the same %s call %d times in a row with no file changed", r.Tools[0], r.Repeats)
	}
	return fmt.Sprintf("the same %d calls (%s) %d times in a row with no file changed",
		r.Period, strings.Join(r.Tools, ", "), r.Repeats)
}

// cosmeticKeys are input keys that describe a call without changing it:
// Claude Code's Bash takes a description the agent rewords on each retry.
var cosmeticKeys = []string{"description"}

// HashToolInput hashes a tool's input so equal calls hash equal: the JSON
// is read and written back with its keys sorted, and cosmetic keys are
// dropped. The hash is all that is kept; the input never is. Input that is
// not a JSON object is hashed as it is.
func HashToolInput(raw json.RawMessage) string {
	canonical := []byte("{}")
	var obj map[string]any
	if len(raw) > 0 && json.Unmarshal(raw, &obj) == nil {
		for _, k := range cosmeticKeys {
			delete(obj, k)
		}
		if b, err := json.Marshal(obj); err == nil {
			canonical = b
		}
	} else if len(raw) > 0 && string(raw) != "null" {
		canonical = raw
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:8])
}

// parseLoopCalls reads a loop_calls threshold. Only > and >= make sense: a
// loop is something that grows. The figure counts at least two calls and
// at most LoopWindow.
func parseLoopCalls(op Op, value string) (float64, error) {
	if op != OpGE && op != OpGT {
		return 0, fmt.Errorf("loop_calls only takes >= or >")
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number", value)
	}
	if n < 2 || n > LoopWindow {
		return 0, fmt.Errorf("%d must be between 2 and %d", n, LoopWindow)
	}
	return float64(n), nil
}
