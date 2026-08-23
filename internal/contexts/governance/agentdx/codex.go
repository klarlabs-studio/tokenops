package agentdx

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
		out = append(out, readCodexTranscript(f, sessionIDFromPath(path), opts.Since)...)
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

func readCodexTranscript(r io.Reader, sessionID string, since time.Time) []Record {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	var (
		out []Record
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
				out = append(out, base)
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
				// Only assistant messages are turns. A user-role item
				// duplicates the user_message event, and a developer-role
				// one is injected scaffolding — counting either would
				// inflate every per-instruction metric.
				if !strings.EqualFold(e.Payload.Role, "assistant") {
					continue
				}
				base.Kind = KindAssistantTurn
				base.InputTokens = pendingContext
				pendingContext = 0
				out = append(out, base)
			}
		}
	}
	return out
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
