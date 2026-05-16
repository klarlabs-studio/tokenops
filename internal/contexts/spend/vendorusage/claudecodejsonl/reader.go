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
// MessageID alone (Anthropic guarantees uniqueness).
type Turn struct {
	Timestamp                time.Time
	SessionID                string
	Model                    string
	MessageID                string
	InputTokens              int64
	OutputTokens             int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
	ServiceTier              string
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
		Usage struct {
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
	return readReader(f, visit)
}

func readReader(r io.Reader, visit func(Turn) error) error {
	scanner := bufio.NewScanner(r)
	// JSONL lines can be large (full conversation history; observed
	// 15 MB+ files). Bump buffer to 4 MB per line.
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
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
		if err := visit(Turn{
			Timestamp:                ts.UTC(),
			SessionID:                raw.SessionID,
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
