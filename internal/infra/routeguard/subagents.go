package routeguard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
)

// Launches is how many subagents a session started, as its transcript
// records them.
type Launches struct {
	// Session is every subagent the session started; Prompt, those since
	// the operator's last prompt. PromptFound is false while the
	// transcript holds no prompt.
	Session     int
	Prompt      int
	PromptFound bool
}

// boundaryRefusal marks a call a TokenOps boundary refused: its reason
// opens with it, wherever Claude Code places it in the result.
const boundaryRefusal = `tokenops boundary "`

// SubagentLaunches counts the subagents a Claude Code session started
// before the call except, which is about to start and is left out
// wherever the transcript already holds it. A launch the operator
// declined, Claude Code stopped or a boundary refused never started and
// does not count; one with no result yet does. Entries a subagent wrote
// (isSidechain) are skipped, and prompts are read in memory only. ok is
// false when the transcript cannot be read.
//
// Only the lines that can matter are decoded: a subagent call names its
// tool, and a prompt or a result is a user entry.
func SubagentLaunches(path, except string) (Launches, bool) {
	if path == "" {
		return Launches{}, false
	}
	f, err := os.Open(path) //nolint:gosec // the operator's own transcript
	if err != nil {
		return Launches{}, false
	}
	defer func() { _ = f.Close() }()
	var (
		order   []string
		started = map[string]bool{}
		since   = 0
		found   = false
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		user := bytes.Contains(line, []byte(`"type":"user"`))
		if !user && !bytes.Contains(line, []byte(`"tool_use"`)) {
			continue
		}
		var e timelineEntry
		if json.Unmarshal(line, &e) != nil || e.IsSidechain {
			continue
		}
		switch e.Type {
		case "assistant":
			for _, id := range subagentCalls(e) {
				if id == except || started[id] {
					continue
				}
				started[id] = true
				order = append(order, id)
			}
		case "user":
			prompt, refused := readLaunchResults(e)
			for _, id := range refused {
				started[id] = false
			}
			if prompt {
				since, found = len(order), true
			}
		}
	}
	var out Launches
	for i, id := range order {
		if !started[id] {
			continue
		}
		out.Session++
		if found && i >= since {
			out.Prompt++
		}
	}
	out.PromptFound = found
	return out, true
}

// subagentCalls are the ids of the subagents an assistant entry starts.
func subagentCalls(e timelineEntry) []string {
	var parts []timelinePart
	if json.Unmarshal(e.Message.Content, &parts) != nil {
		return nil
	}
	var out []string
	for _, p := range parts {
		if p.Type == "tool_use" && p.ID != "" && subagentTools[strings.ToLower(p.Name)] {
			out = append(out, p.ID)
		}
	}
	return out
}

// readLaunchResults reads a user entry: whether it is the operator's
// prompt, and the calls whose result says they never ran.
func readLaunchResults(e timelineEntry) (prompt bool, refused []string) {
	if e.IsCompact {
		return false, nil
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) == nil {
		return isPrompt(text), nil
	}
	var parts []timelinePart
	if json.Unmarshal(e.Message.Content, &parts) != nil {
		return false, nil
	}
	var b strings.Builder
	results := false
	for _, p := range parts {
		switch p.Type {
		case "tool_result":
			results = true
			body := resultText(p.Content)
			if o, _ := classify(p.IsError, body); o != OutcomeRan || (p.IsError && strings.Contains(body, boundaryRefusal)) {
				refused = append(refused, p.ToolUseID)
			}
		case "text":
			b.WriteString(p.Text)
			b.WriteByte(' ')
		}
	}
	return !results && isPrompt(b.String()), refused
}
