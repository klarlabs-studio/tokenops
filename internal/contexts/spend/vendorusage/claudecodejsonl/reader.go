// Package claudecodejsonl reads Claude Code's per-session conversation
// JSONL files (~/.claude/projects/<project>/<session>.jsonl) and emits
// one PromptEvent per assistant turn. The JSONLs are the canonical
// live record of every prompt/response Claude Code makes — the file is
// updated on every turn, so a poll catches activity within seconds.
//
// This replaces the v0.10.2 stats-cache reader (which read
// ~/.claude/stats-cache.json — that file lags by days on active users
// and is effectively useless as a live signal). Both pollers can run
// side-by-side during the transition; the stats-cache one is now
// deprecated and will be removed in a future release.
//
// Each JSONL line is a single conversation turn:
//
//	{
//	  "type": "assistant",
//	  "timestamp": "2026-05-14T09:22:45.151Z",
//	  "sessionId": "...",
//	  "message": {
//	    "id": "msg_...",
//	    "model": "claude-opus-4-7",
//	    "usage": {
//	      "input_tokens": 1,
//	      "output_tokens": 240,
//	      "cache_read_input_tokens": 755946,
//	      "cache_creation_input_tokens": 569
//	    }
//	  }
//	}
//
// We only emit on "assistant" turns (user turns have no usage block).
// Dedup is by message.id so concurrent sessions merge cleanly.
package claudecodejsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn is one parsed assistant turn from a JSONL file. SessionID +
// MessageID together uniquely identify the turn; callers dedupe on
// MessageID alone (Anthropic guarantees uniqueness). Project is the
// filesystem-encoded project directory name from
// ~/.claude/projects/<project>/<session>.jsonl — surfaces per-project
// rollups via agent_id/workflow_id attribution downstream.
type Turn struct {
	Timestamp                time.Time
	SessionID                string
	Project                  string
	Model                    string
	MessageID                string
	InputTokens              int64
	OutputTokens             int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
	ServiceTier              string
	// StartsUserMessage marks the first assistant turn produced in
	// response to a prompt the operator actually typed. One prompt fans
	// out into many assistant turns, so exactly one turn per prompt
	// carries this — it is what lets the plan window meter count the
	// vendor's "messages" unit from a per-turn event stream.
	StartsUserMessage bool
}

// rawLine is the minimal subset of a Claude Code JSONL row we care
// about. Unknown fields are ignored so a future Claude Code release
// adding keys won't break the parser.
type rawLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		// Content is a string for a typed prompt and an array of parts
		// otherwise. Kept raw so isOperatorPrompt can tell a real prompt
		// from the tool-result echoes that share type:"user".
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens              int64  `json:"input_tokens"`
			OutputTokens             int64  `json:"output_tokens"`
			CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
			ServiceTier              string `json:"service_tier"`
		} `json:"usage"`
	} `json:"message"`
}

// DefaultRoot returns the conventional Claude Code projects directory
// (~/.claude/projects). Operators may override via the config block.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// FindSessionFiles globs every *.jsonl under root (recursive, one
// level deep — matches Claude Code's actual layout). Returns paths
// sorted lexicographically for deterministic iteration in tests.
func FindSessionFiles(root string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("glob session files: %w", err)
	}
	return matches, nil
}

// ReadFile parses one JSONL file and yields every assistant turn with
// a usage block via the visit callback. Lines that don't parse, lack
// a timestamp, or aren't assistant turns are silently skipped — the
// JSONL contains user / system / tool-use turns that don't carry
// usage data, and we don't want a single malformed line to abort the
// whole scan. The visit callback returning a non-nil error aborts.
func ReadFile(path string, visit func(Turn) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	project := projectFromPath(path)
	return readReader(f, project, visit)
}

// projectFromPath returns the project directory name for a JSONL
// path. Claude Code lays out files as
// ~/.claude/projects/<project>/<session>.jsonl where <project> is
// the filesystem-encoded project root (slashes → dashes). The name
// is returned verbatim — operators recognise their own encoding.
func projectFromPath(path string) string {
	return filepath.Base(filepath.Dir(path))
}

func readReader(r io.Reader, project string, visit func(Turn) error) error {
	scanner := bufio.NewScanner(r)
	// JSONL lines can be large (full conversation history; observed
	// 15 MB+ files). Bump buffer to 4 MB per line.
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 4*1024*1024)
	// Set when a typed prompt is seen; consumed by the next emitted
	// assistant turn.
	var pendingUserMessage bool
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if strings.EqualFold(raw.Type, "user") {
			// Claude Code writes tool results back as type:"user" rows;
			// in real sessions they outnumber typed prompts by roughly
			// 44:1. Only a genuine prompt opens a new vendor "message".
			if isOperatorPrompt(raw.Message.Content) {
				pendingUserMessage = true
			}
			continue
		}
		if !strings.EqualFold(raw.Type, "assistant") {
			continue
		}
		if raw.Message.ID == "" {
			continue
		}
		// Skip turns with zero usage — these are no-op tool-result
		// echoes Claude Code emits without a real model call.
		u := raw.Message.Usage
		if u.InputTokens == 0 && u.OutputTokens == 0 &&
			u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0 {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
		if err != nil {
			continue
		}
		// Consume the boundary on the first turn we actually emit, so a
		// skipped zero-usage echo does not swallow the message.
		startsMessage := pendingUserMessage
		pendingUserMessage = false
		if err := visit(Turn{
			StartsUserMessage:        startsMessage,
			Timestamp:                ts.UTC(),
			SessionID:                raw.SessionID,
			Project:                  project,
			Model:                    raw.Message.Model,
			MessageID:                raw.Message.ID,
			InputTokens:              u.InputTokens,
			OutputTokens:             u.OutputTokens,
			CacheReadInputTokens:     u.CacheReadInputTokens,
			CacheCreationInputTokens: u.CacheCreationInputTokens,
			ServiceTier:              u.ServiceTier,
		}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// isOperatorPrompt reports whether a type:"user" row is a prompt the
// operator typed rather than a tool result the agent fed back. String
// content is always a prompt; array content counts only when it holds a
// part that is not a tool_result.
func isOperatorPrompt(content json.RawMessage) bool {
	if len(content) == 0 {
		return false
	}
	var asString string
	if json.Unmarshal(content, &asString) == nil {
		return strings.TrimSpace(asString) != ""
	}
	var parts []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return false
	}
	for _, p := range parts {
		if p.Type != "tool_result" {
			return true
		}
	}
	return false
}
