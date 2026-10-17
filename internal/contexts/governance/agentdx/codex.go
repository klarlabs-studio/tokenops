package agentdx

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CodexDefaultRoot returns the conventional Codex CLI sessions directory.
func CodexDefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
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
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
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
			continue
		}
		if cache != nil {
			kept[path] = true
			out = append(out, cache.codex(path, info, opts.Since)...)
			continue
		}
		f, err := os.Open(path) //nolint:gosec // operator's own transcript dir
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
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if i := strings.LastIndex(name, "-"); i >= 0 && i+1 < len(name) {
		// The uuid's last segment alone is not unique enough; keep the
		// tail from the timestamp onward.
		if j := strings.Index(name, "rollout-"); j == 0 {
			return name[len("rollout-"):]
		}
	}
	return name
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
	out, _ := readCodexTranscriptFrom(r, sessionID, since, withText)
	return out
}

// readCodexTranscriptFrom is readCodexTranscript, plus the earliest time
// any line of the file carries (zero when none has one). A line before
// since still sets nothing, so a file whose every line is at or after two
// cut-offs reads the same under either.
func readCodexTranscriptFrom(r io.Reader, sessionID string, since time.Time, withText bool) ([]Record, time.Time) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxTranscriptLine)
	var first time.Time
	var (
		out []Record
		// Prompts from each channel are kept apart until the whole file
		// has been read, because "does this file use response_items"
		// cannot be answered from the first line.
		eventPrompts []Record
		itemPrompts  []Record
		// Context size is reported by its own event, just before the turn
		// it describes; carry it to the next assistant message.
		pendingContext int64
		// The model and effort the current turn runs under, from its
		// turn_context line.
		model, effort string
		// role is what the session runs as, from its session_meta line.
		role string
	)
	for sc.Scan() {
		var e codexLine
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			continue
		}
		at = at.UTC()
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if !since.IsZero() && at.Before(since) {
			continue
		}
		base := Record{At: at, SessionID: sessionID}

		switch e.Type {
		case "session_meta":
			role = codexSessionRole(e.Payload.Source)
		case "turn_context":
			model, effort = e.Payload.Model, normEffort(e.Payload.Effort)
		case "event_msg":
			switch e.Payload.Type {
			case "user_message":
				base.Kind = KindPrompt
				base.Role = role
				if withText {
					base.Text = e.Payload.Message
				}
				eventPrompts = append(eventPrompts, base)
			case "turn_aborted":
				base.Kind = KindInterrupt
				out = append(out, base)
			case "task_complete":
				base.Kind = KindTurnEnd
				base.DurationMs = e.Payload.DurationMs
				if e.Payload.TTFTMs != nil {
					base.TTFTMs, base.TTFTRecorded = *e.Payload.TTFTMs, true
				}
				out = append(out, base)
			case "token_count":
				u := e.Payload.Info.LastTokenUsage
				pendingContext = u.InputTokens + u.CachedInputTokens + u.CacheWriteTokens
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
				out = append(out, base)
			case "function_call_output", "custom_tool_call_output":
				if e.Payload.CallID != "" {
					base.Kind = KindToolResult
					base.CallID = e.Payload.CallID
					out = append(out, base)
				}
			case "message":
				switch {
				case strings.EqualFold(e.Payload.Role, "assistant"):
					base.Kind = KindAssistantTurn
					base.Model, base.Effort = model, effort
					base.InputTokens = pendingContext
					pendingContext = 0
					out = append(out, base)
				case strings.EqualFold(e.Payload.Role, "user"):
					// A developer-role message is injected scaffolding and
					// is never an instruction; a user-role one usually is.
					text := codexMessageText(e.Payload.Content)
					if isCodexScaffolding(text) {
						continue
					}
					base.Kind = KindPrompt
					base.Role = role
					if withText {
						base.Text = text
					}
					itemPrompts = append(itemPrompts, base)
				}
			}
		}
	}
	// One channel, chosen by what the file actually contains.
	prompts := itemPrompts
	if len(prompts) == 0 {
		prompts = eventPrompts
	}
	out = append(out, prompts...)
	// Prompts were held back, so append order is no longer file order.
	// Everything downstream — unit grouping, idle gaps, the story's
	// boundaries — reads these in sequence.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, first
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
