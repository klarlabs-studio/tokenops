package routeguard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/moveverdict"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
)

// SessionStart is when a session began and the model family it started
// on, as its transcript records them.
type SessionStart struct {
	At    time.Time
	Model string
}

// AliasFor is the model family (Agent-tool alias: haiku, sonnet, opus)
// a model id belongs to, or "" when it has none.
func AliasFor(model string) string { return aliasFor(model) }

// subagentTools launch a subagent (Claude Code has named it both).
var subagentTools = map[string]bool{"agent": true, "task": true}

// parentEditTools modify a file.
var parentEditTools = map[string]bool{"edit": true, "write": true, "multiedit": true, "notebookedit": true}

// pathMention is a file path a brief or report names: a word with a
// letters-only extension, optionally with directories.
var pathMention = regexp.MustCompile(`(?:[\w.-]+/)*[\w-][\w.-]*\.[A-Za-z]{1,6}\b`)

// interruptText is what Claude Code writes when the operator stops a turn.
const interruptText = "[request interrupted"

// ReadTimeline reads what a Claude Code session transcript says about
// its subagents: each Agent call with its task kind, the model family it
// ran on and its result, and around them the operator's instructions,
// interrupts and the parent's own edits. Entries a subagent wrote
// (isSidechain) are skipped. Prompt text is read in memory only. A file
// that cannot be read yields an empty timeline.
func ReadTimeline(path string) (moveverdict.Timeline, SessionStart) {
	tl := moveverdict.Timeline{Scope: filepath.Base(filepath.Dir(path))}
	var start SessionStart
	f, err := os.Open(path) //nolint:gosec // the operator's own transcript
	if err != nil {
		return tl, start
	}
	defer func() { _ = f.Close() }()
	idx := map[string]int{}
	model := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var e timelineEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.IsSidechain {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			continue
		}
		at = at.UTC()
		if start.At.IsZero() {
			start.At = at
		}
		if tl.Session == "" {
			tl.Session = e.SessionID
		}
		switch e.Type {
		case "assistant":
			if e.Message.Model != "" {
				model = e.Message.Model
				fam := aliasFor(model)
				switch {
				case start.Model == "":
					start.Model = fam
				case fam != start.Model && tl.ModelSwitch.IsZero():
					tl.ModelSwitch = at
				}
			}
			readAssistant(&tl, idx, e, at, model)
		case "user":
			readUser(&tl, idx, e, at)
		}
	}
	return tl, start
}

// ReadStart reads only as far as a session transcript's first model: when
// the session began and the family it started on. Cheap enough for every
// turn; ReadTimeline reads the whole file.
func ReadStart(path string) SessionStart {
	var start SessionStart
	f, err := os.Open(path) //nolint:gosec // the operator's own transcript
	if err != nil {
		return start
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var e timelineEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.IsSidechain {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			continue
		}
		if start.At.IsZero() {
			start.At = at.UTC()
		}
		if e.Type == "assistant" && e.Message.Model != "" {
			start.Model = aliasFor(e.Message.Model)
			return start
		}
	}
	return start
}

type timelineEntry struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	SessionID   string `json:"sessionId"`
	IsSidechain bool   `json:"isSidechain"`
	IsCompact   bool   `json:"isCompactSummary"`
	Message     struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type timelinePart struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
	Input     struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Model       string `json:"model"`
		FilePath    string `json:"file_path"`
	} `json:"input"`
}

func readAssistant(tl *moveverdict.Timeline, idx map[string]int, e timelineEntry, at time.Time, sessionModel string) {
	var parts []timelinePart
	if json.Unmarshal(e.Message.Content, &parts) != nil {
		return
	}
	for _, p := range parts {
		if p.Type != "tool_use" {
			continue
		}
		name := strings.ToLower(p.Name)
		switch {
		case subagentTools[name] && p.ID != "":
			kind := taskclass.KindForTurn(p.Input.Description, "")
			if kind == "" {
				kind = taskclass.KindForTurn(p.Input.Prompt, "")
			}
			family := sessionModel
			if r := strings.TrimSpace(p.Input.Model); r != "" && !strings.EqualFold(r, "inherit") {
				family = r
			}
			idx[p.ID] = len(tl.Calls)
			tl.Calls = append(tl.Calls, moveverdict.Call{
				Ref: p.ID, At: at, Kind: string(kind), Model: aliasFor(family),
				Words: strings.ToLower(p.Input.Description), Mentions: mentions(p.Input.Prompt),
			})
		case parentEditTools[name] && p.Input.FilePath != "":
			tl.Edits = append(tl.Edits, moveverdict.Edit{At: at, File: p.Input.FilePath})
		}
	}
}

func readUser(tl *moveverdict.Timeline, idx map[string]int, e timelineEntry, at time.Time) {
	if e.IsCompact {
		return
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) == nil {
		addPrompt(tl, text, at)
		return
	}
	var parts []timelinePart
	if json.Unmarshal(e.Message.Content, &parts) != nil {
		return
	}
	var b strings.Builder
	results := false
	for _, p := range parts {
		switch p.Type {
		case "tool_result":
			results = true
			i, ok := idx[p.ToolUseID]
			if !ok {
				continue
			}
			c := &tl.Calls[i]
			body := resultText(p.Content)
			outcome, _ := classify(p.IsError, body)
			c.ResultAt, c.HasResult = at, true
			c.Excluded = outcome != OutcomeRan
			c.Failed = outcome == OutcomeRan && p.IsError
			c.Mentions = append(c.Mentions, mentions(body)...)
		case "text":
			b.WriteString(p.Text)
			b.WriteByte(' ')
		}
	}
	if !results {
		addPrompt(tl, b.String(), at)
	}
}

func addPrompt(tl *moveverdict.Timeline, text string, at time.Time) {
	t := strings.TrimSpace(text)
	switch {
	case t == "":
	case strings.Contains(strings.ToLower(t), interruptText):
		tl.Interrupts = append(tl.Interrupts, at)
	case strings.HasPrefix(t, "<"), agentdx.IsInjected(t):
		// A slash command, reminder or notification the harness wrote.
	default:
		tl.Prompts = append(tl.Prompts, moveverdict.Prompt{At: at, Rejects: agentdx.IsRejection(t)})
	}
}

// mentions are the file paths named in text, at most a few dozen.
func mentions(text string) []string {
	found := pathMention.FindAllString(text, 48)
	out := found[:0]
	for _, m := range found {
		if len(m) >= 4 {
			out = append(out, m)
		}
	}
	return out
}
