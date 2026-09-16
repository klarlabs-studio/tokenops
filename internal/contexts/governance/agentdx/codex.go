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
	for _, path := range files {
		f, err := os.Open(path) //nolint:gosec // operator's own transcript dir
		if err != nil {
			continue
		}
		out = append(out, readCodexTranscript(f, sessionIDFromPath(path), opts.Since, opts.WithPromptText)...)
		_ = f.Close()
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
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Name    string          `json:"name"`
		Message string          `json:"message"`
		Content json.RawMessage `json:"content"`
		Info    struct {
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
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
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
		if !since.IsZero() && at.Before(since) {
			continue
		}
		base := Record{At: at, SessionID: sessionID}

		switch e.Type {
		case "event_msg":
			switch e.Payload.Type {
			case "user_message":
				base.Kind = KindPrompt
				if withText {
					base.Text = e.Payload.Message
				}
				eventPrompts = append(eventPrompts, base)
			case "turn_aborted":
				base.Kind = KindInterrupt
				out = append(out, base)
			case "token_count":
				u := e.Payload.Info.LastTokenUsage
				pendingContext = u.InputTokens + u.CachedInputTokens + u.CacheWriteTokens
			}
		case "response_item":
			switch e.Payload.Type {
			case "function_call":
				base.Kind = KindToolUse
				base.ToolName = e.Payload.Name
				base.FilePath = codexFilePath(e.Payload.Content)
				out = append(out, base)
			case "message":
				switch {
				case strings.EqualFold(e.Payload.Role, "assistant"):
					base.Kind = KindAssistantTurn
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
	return out
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
