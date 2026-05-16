// Package prompts walks Claude Code JSONL session logs and yields
// user-typed prompt text. Reads the files directly (same source the
// claudecodejsonl poller uses) so prompt text never lands in the
// event store — keeps the analyzer privacy-respecting and avoids
// schema bloat for the >99% of operators who don't need it.
package prompts

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UserPrompt is one human-typed turn from a Claude Code session log.
// Text is the raw prompt content with no normalisation — downstream
// analyzers do their own casing / trimming.
type UserPrompt struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	Text      string    `json:"text"`
}

// ExtractOptions filters the walk. Zero values mean "no filter".
type ExtractOptions struct {
	Root      string    // base directory; defaults to ~/.claude/projects when empty
	Since     time.Time // include turns at or after this instant
	Until     time.Time // include turns at or before this instant; zero = open
	SessionID string    // restrict to one session (matches the filename stem)
	Limit     int       // max prompts to return; 0 = unbounded
}

// turnsScanBufSize matches the claudecodejsonl reader so very large
// files (Cursor + Claude Code regularly push past the default 64KB
// line ceiling) don't truncate mid-record.
const turnsScanBufSize = 4 * 1024 * 1024

// rawLine is the minimum subset of a JSONL turn we need to identify
// user-typed prompts. Tool results + slash-command meta are filtered
// out at the value layer.
type rawLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// rawMessage is the inner `message` block. Claude Code emits content
// as either a string (early format) or an array of typed parts
// (current format).
type rawMessage struct {
	Content json.RawMessage `json:"content"`
}

// rawContentPart matches one entry of message.content[] when content
// is an array. We only care about the "text" parts.
type rawContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Extract walks opts.Root, parses every .jsonl file, and returns the
// user-typed prompts that pass the filters. Files unreadable for
// permission reasons are skipped (best-effort scanning beats failing
// the whole report). Parse errors are skipped per-line so one bad
// turn in a 15MB file doesn't kill the walk.
func Extract(opts ExtractOptions) ([]UserPrompt, error) {
	root := opts.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".claude", "projects")
	}
	var out []UserPrompt
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if opts.SessionID != "" {
			stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			if stem != opts.SessionID {
				return nil
			}
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), turnsScanBufSize)
		for scanner.Scan() {
			var line rawLine
			if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
				continue
			}
			if line.Type != "user" {
				continue
			}
			ts, terr := time.Parse(time.RFC3339Nano, line.Timestamp)
			if terr != nil {
				continue
			}
			if !opts.Since.IsZero() && ts.Before(opts.Since) {
				continue
			}
			if !opts.Until.IsZero() && ts.After(opts.Until) {
				continue
			}
			text := decodeContent(line.Message)
			if text == "" || isSyntheticMeta(text) {
				continue
			}
			out = append(out, UserPrompt{
				Timestamp: ts,
				SessionID: line.SessionID,
				Text:      text,
			})
			if opts.Limit > 0 && len(out) >= opts.Limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return nil, err
	}
	return out, nil
}

// decodeContent extracts user prompt text from message.content,
// handling both shapes (raw string + typed-part array). Concatenates
// when multiple text parts coexist in one turn.
func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var msg rawMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ""
	}
	if len(msg.Content) == 0 {
		return ""
	}
	// String form.
	if msg.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil {
			return s
		}
		return ""
	}
	// Array form.
	if msg.Content[0] == '[' {
		var parts []rawContentPart
		if err := json.Unmarshal(msg.Content, &parts); err != nil {
			return ""
		}
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

// isSyntheticMeta filters lines that wouldn't be useful for prompt
// coaching: tool-result payloads, system messages injected by the
// host, and slash-command rendering. These are emitted by Claude
// Code as type=user but they are not human prompts.
func isSyntheticMeta(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	if strings.HasPrefix(t, "<") {
		// e.g. "<command-name>", "<system-reminder>", "<tool-use ...>"
		return true
	}
	if strings.Contains(t, "tool_use_id") {
		return true
	}
	return false
}
