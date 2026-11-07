package agentdx

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexhome"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexrollout"
)

// CodexDefaultRoot returns the conventional Codex CLI sessions directory.
func CodexDefaultRoot() (string, error) {
	return codexhome.SessionsDir()
}

// ExtractCodex reads Codex CLI rollout transcripts into Records.
//
// Codex marks the things these metrics need explicitly, where Claude Code
// leaves them to be inferred: a `user_message` event is unambiguously the
// operator's instruction, and `turn_aborted` is unambiguously an
// interrupt. The Claude Code reader has to tell typed prompts from
// tool-result echoes by content shape and spot interrupts by matching a
// text marker; neither guess is needed here.
func ExtractCodex(opts ExtractOptions) ([]Record, error) {
	root := opts.Root
	if root == "" {
		r, err := CodexDefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	var files []string
	// Codex nests rollouts under year/month/day, so walk rather than glob
	// a fixed depth.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if !d.IsDir() && codexrollout.IsRolloutName(path) {
			files = append(files, path)
		}
		return nil
	})

	var out []Record
	cache := opts.cache()
	kept := make(map[string]bool, len(files))
	for _, path := range files {
		info, fresh := writtenSince(path, opts.Since)
		if !fresh {
			// Too old for this scan but within what the cache keeps for
			// a longer one: it stays cached.
			if cache.holds(path, opts.Since) {
				kept[path] = true
			}
			continue
		}
		if cache != nil {
			kept[path] = true
			out = append(out, cache.codex(path, info, opts.Since)...)
			continue
		}
		f, err := codexrollout.Open(path)
		if err != nil {
			continue
		}
		out = append(out, readCodexTranscript(f, sessionIDFromPath(path), opts.Since, opts.WithPromptText)...)
		_ = f.Close()
	}
	if cache != nil {
		cache.retain(sourceCodex, kept)
	}
	return out, nil
}

// sessionIDFromPath recovers the session id from a rollout filename of
// the form rollout-<timestamp>-<uuid>.jsonl.
func sessionIDFromPath(path string) string {
	if id, ok := codexrollout.SessionID(path); ok {
		return id
	}
	return strings.TrimSuffix(filepath.Base(path), codexrollout.PlainSuffix)
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Name    string `json:"name"`
		CallID  string `json:"call_id"`
		Message string `json:"message"`
		// Source is set on session_meta: "vscode", "cli", or an object
		// naming the subagent the session runs as.
		Source json.RawMessage `json:"source"`
		// DurationMs and TTFTMs are set on task_complete: the harness's
		// own measure of the turn it closes.
		DurationMs int64           `json:"duration_ms"`
		TTFTMs     *int64          `json:"time_to_first_token_ms"`
		Content    json.RawMessage `json:"content"`
		// Model and Effort are set on turn_context lines, which open each
		// turn with the settings it runs under.
		Model  string `json:"model"`
		Effort string `json:"effort"`
		Info   struct {
			LastTokenUsage struct {
				InputTokens       int64 `json:"input_tokens"`
				CachedInputTokens int64 `json:"cached_input_tokens"`
				CacheWriteTokens  int64 `json:"cache_write_input_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// readCodexTranscript flattens one rollout file.
//
// Codex records an operator instruction in one of two places depending on
// its version: an `event_msg`/`user_message` event, or a `response_item`
// message with role "user". Older rollouts carry both, which is why this
// reader used to count the event and skip the item — counting both would
// have doubled every instruction.
//
// Current Codex emits only the response_item. Skipping it unconditionally
// therefore counted nothing at all: on 37 real sessions holding 343
// instructions, `tokenops dx --source codex` reported "0 instructions
// across 37 sessions". It found the files, parsed them, and measured
// nothing — a silent under-read wearing the clothes of an idle machine.
//
// So the channel is decided per file rather than assumed: whichever of
// the two a file actually uses is the one counted, and a file carrying
// both counts the response_item, which is also the only one with the
// instruction's text on it.
func readCodexTranscript(r io.Reader, sessionID string, since time.Time, withText bool) []Record {
	cr := newCodexReader(sessionID, since, withText, nil)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxTranscriptLine)
	for sc.Scan() {
		cr.line(sc.Bytes())
	}
	return cr.records()
}

// codexReader flattens a rollout one line at a time. A rollout carries
// state from line to line (what the session runs as, the model of the
// turn, the context size before it, which channel it records prompts on),
// so the reader is kept between lines, and the transcript cache keeps it
// between scans to read only what was appended.
//
// State is read from every line; the cut-off only decides which records
// are kept. A read with a cut-off therefore yields exactly a full read's
// records at or after it, and a subagent session that started before the
// cut-off still reads as a subagent.
type codexReader struct {
	sessionID string
	since     time.Time
	withText  bool
	facts     PromptFactsFunc

	out []Record
	// Prompts from each channel are kept apart until the records are
	// asked for, because "does this file use response_items" cannot be
	// answered from the first line. itemSeen remembers the answer once
	// the prompts that gave it have been dropped.
	eventPrompts []Record
	itemPrompts  []Record
	itemSeen     bool
	// Context size is reported by its own event, just before the turn
	// it describes; carry it to the next assistant message.
	pendingContext int64
	// The model and effort the current turn runs under, from its
	// turn_context line.
	model, effort string
	// role is what the session runs as, from its session_meta line.
	role string
}

func newCodexReader(sessionID string, since time.Time, withText bool, facts PromptFactsFunc) *codexReader {
	return &codexReader{sessionID: sessionID, since: since, withText: withText, facts: facts}
}

// line reads one line of the rollout.
func (c *codexReader) line(b []byte) {
	var e codexLine
	if json.Unmarshal(b, &e) != nil {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return
	}
	at = at.UTC()
	keep := c.since.IsZero() || !at.Before(c.since)
	base := Record{At: at, SessionID: c.sessionID}
	emit := func(dst *[]Record, r Record) {
		if keep {
			*dst = append(*dst, r)
		}
	}

	switch e.Type {
	case "session_meta":
		c.role = codexSessionRole(e.Payload.Source)
	case "turn_context":
		c.model, c.effort = e.Payload.Model, normEffort(e.Payload.Effort)
	case "event_msg":
		switch e.Payload.Type {
		case "user_message":
			base.Kind = KindPrompt
			base.Role = c.role
			if c.withText {
				base.Text = e.Payload.Message
			}
			if c.facts != nil {
				base.Facts = c.facts(e.Payload.Message)
			}
			emit(&c.eventPrompts, base)
		case "turn_aborted":
			base.Kind = KindInterrupt
			emit(&c.out, base)
		case "task_complete":
			base.Kind = KindTurnEnd
			base.DurationMs = e.Payload.DurationMs
			if e.Payload.TTFTMs != nil {
				base.TTFTMs, base.TTFTRecorded = *e.Payload.TTFTMs, true
			}
			emit(&c.out, base)
		case "token_count":
			u := e.Payload.Info.LastTokenUsage
			c.pendingContext = u.InputTokens + u.CachedInputTokens + u.CacheWriteTokens
		}
	case "response_item":
		switch e.Payload.Type {
		case "function_call", "custom_tool_call":
			// custom_tool_call is how current Codex issues its own
			// tools (apply_patch among them); reading function_call
			// alone missed most of a session's calls.
			base.Kind = KindToolUse
			base.ToolName = e.Payload.Name
			base.CallID = e.Payload.CallID
			base.FilePath = codexFilePath(e.Payload.Content)
			emit(&c.out, base)
		case "function_call_output", "custom_tool_call_output":
			if e.Payload.CallID != "" {
				base.Kind = KindToolResult
				base.CallID = e.Payload.CallID
				emit(&c.out, base)
			}
		case "message":
			switch {
			case strings.EqualFold(e.Payload.Role, "assistant"):
				base.Kind = KindAssistantTurn
				base.Model, base.Effort = c.model, c.effort
				base.InputTokens = c.pendingContext
				c.pendingContext = 0
				emit(&c.out, base)
			case strings.EqualFold(e.Payload.Role, "user"):
				// A developer-role message is injected scaffolding and
				// is never an instruction; a user-role one usually is.
				text := codexMessageText(e.Payload.Content)
				if isCodexScaffolding(text) {
					return
				}
				c.itemSeen = true
				base.Kind = KindPrompt
				base.Role = c.role
				if c.withText {
					base.Text = text
				}
				if c.facts != nil {
					base.Facts = c.facts(text)
				}
				emit(&c.itemPrompts, base)
			}
		}
	}
}

// records returns what the rollout yields so far, in time order, without
// changing the reader.
func (c *codexReader) records() []Record {
	// One channel, chosen by what the file actually contains.
	prompts := c.itemPrompts
	if !c.itemSeen {
		prompts = c.eventPrompts
	}
	out := make([]Record, 0, len(c.out)+len(prompts))
	out = append(out, c.out...)
	out = append(out, prompts...)
	// Prompts were held back, so append order is no longer file order.
	// Everything downstream — unit grouping, idle gaps, the story's
	// boundaries — reads these in sequence.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// fork returns a copy whose further lines leave c as it is: a final line
// still being written is read on the copy, and read again on c once it is
// finished.
func (c *codexReader) fork() *codexReader {
	f := *c
	f.out, f.eventPrompts, f.itemPrompts = slices.Clip(c.out), slices.Clip(c.eventPrompts), slices.Clip(c.itemPrompts)
	return &f
}

// advance moves the cut-off forward to since and forgets the records
// before it. A cut-off earlier than the reader's is not reachable; the
// caller reads the file again.
func (c *codexReader) advance(since time.Time) {
	if !since.After(c.since) {
		return
	}
	c.since = since
	c.out = dropBefore(c.out, since)
	c.eventPrompts = dropBefore(c.eventPrompts, since)
	c.itemPrompts = dropBefore(c.itemPrompts, since)
}

// codexSessionRole reads what a session runs as from session_meta's
// source. Codex writes a plain string for a session the operator opened
// ("vscode", "cli") and an object for one it spawned: the automatic
// reviewer ("guardian") or a delegated thread.
func codexSessionRole(raw json.RawMessage) string {
	var src struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &src) != nil || len(src.Subagent) == 0 {
		return ""
	}
	var named struct {
		Other string `json:"other"`
	}
	if json.Unmarshal(src.Subagent, &named) == nil && strings.EqualFold(named.Other, "guardian") {
		return RoleAutoReview
	}
	return RoleSubagent
}

// codexMessageText joins a message's text parts. Codex carries content as
// `[{"type":"input_text","text":"…"}]`; anything else yields no text,
// which reads downstream as an instruction whose words were not recorded
// rather than as no instruction.
func codexMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// isCodexScaffolding reports whether a user-role message was injected by
// the harness rather than typed by the operator.
//
// Codex delivers plugin advertisements, permission preambles and tool
// results through the same user role a person types into. On real
// history 56 of 399 user-role messages were this. Counting them would
// inflate every per-instruction metric, and the inflation would look
// like the operator giving more instructions than they did.
func isCodexScaffolding(text string) bool {
	t := strings.TrimSpace(text)
	return t == "" || strings.HasPrefix(t, "<") || strings.Contains(t, "tool_use_id")
}

// codexFilePath pulls a file path out of a function call's arguments when
// one is present. Codex passes arguments as a JSON string rather than an
// object, and most calls are shell commands with no file at all, so a
// miss is normal and silent.
func codexFilePath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		raw = json.RawMessage(asString)
	}
	var args struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	return firstNonEmpty(args.FilePath, args.Path)
}
