// Package replies walks Claude Code (and Codex) JSONL session logs
// and yields the assistant's textual responses. Reads the files
// directly (same source the claudecodejsonl poller uses) so reply
// text never lands in the event store — keeps the analyzer privacy-
// respecting and avoids schema bloat.
//
// Sibling to the prompts package; same JSONL traversal, opposite
// role filter. Used by `tokenops coach replies` to detect output-side
// compression patterns (e.g. caveman-style fragmenting) that the
// prompt-side coach cannot see.
package replies

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

// AssistantReply is one model-emitted turn from a session log.
// Text is the raw reply with no normalisation — downstream analyzers
// do their own casing / trimming.
type AssistantReply struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	Text      string    `json:"text"`
}

// Source identifies which JSONL dialect to parse. Mirrors the prompts
// package so a single coach invocation can union both CLIs.
type Source string

const (
	SourceAuto       Source = ""
	SourceClaudeCode Source = "claude-code"
	SourceCodex      Source = "codex"
)

// ExtractOptions filters the walk. Zero values mean "no filter".
type ExtractOptions struct {
	Root      string
	Source    Source
	Since     time.Time
	Until     time.Time
	SessionID string
	Limit     int
}

// turnsScanBufSize mirrors the prompts extractor so large session
// files (often >1MB lines under long sessions) parse without
// truncating mid-record.
const turnsScanBufSize = 4 * 1024 * 1024

type rawLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

type rawMessage struct {
	Content json.RawMessage `json:"content"`
}

type rawContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Extract walks the appropriate JSONL tree and returns assistant
// replies passing the filters. Identical traversal to prompts.Extract;
// only the role filter differs (type == "assistant" / role == "assistant").
func Extract(opts ExtractOptions) ([]AssistantReply, error) {
	if opts.Source == SourceAuto && opts.Root == "" {
		return extractAuto(opts)
	}
	src := opts.Source
	root := opts.Root
	home, _ := os.UserHomeDir()
	if root == "" {
		switch src {
		case SourceCodex:
			root = filepath.Join(home, ".codex", "sessions")
		default:
			root = filepath.Join(home, ".claude", "projects")
			if src == SourceAuto {
				src = SourceClaudeCode
			}
		}
	}
	if src == SourceAuto {
		if strings.Contains(root, ".codex") {
			src = SourceCodex
		} else {
			src = SourceClaudeCode
		}
	}
	return extractFromRoot(root, src, opts)
}

func extractAuto(opts ExtractOptions) ([]AssistantReply, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var out []AssistantReply
	for _, c := range []struct {
		root string
		src  Source
	}{
		{filepath.Join(home, ".claude", "projects"), SourceClaudeCode},
		{filepath.Join(home, ".codex", "sessions"), SourceCodex},
	} {
		if _, statErr := os.Stat(c.root); statErr != nil {
			continue
		}
		got, err := extractFromRoot(c.root, c.src, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return out[:opts.Limit], nil
		}
	}
	return out, nil
}

func extractFromRoot(root string, src Source, opts ExtractOptions) ([]AssistantReply, error) {
	var out []AssistantReply
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
		f, err := os.Open(path) // #nosec G304 -- walking opted-in root
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), turnsScanBufSize)
		sessionFallbackID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		for scanner.Scan() {
			r, ok := parseLine(scanner.Bytes(), src, sessionFallbackID)
			if !ok {
				continue
			}
			if !opts.Since.IsZero() && r.Timestamp.Before(opts.Since) {
				continue
			}
			if !opts.Until.IsZero() && r.Timestamp.After(opts.Until) {
				continue
			}
			out = append(out, r)
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

func parseLine(raw []byte, src Source, fallbackSession string) (AssistantReply, bool) {
	if src == SourceCodex {
		var c struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &c); err != nil || c.Role != "assistant" {
			return AssistantReply{}, false
		}
		var b strings.Builder
		for _, p := range c.Content {
			if p.Type == "output_text" || p.Type == "text" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			}
		}
		text := strings.TrimSpace(b.String())
		if text == "" {
			return AssistantReply{}, false
		}
		return AssistantReply{SessionID: fallbackSession, Text: text}, true
	}

	var line rawLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return AssistantReply{}, false
	}
	if line.Type != "assistant" {
		return AssistantReply{}, false
	}
	text := decodeContent(line.Message)
	if text == "" {
		return AssistantReply{}, false
	}
	ts, _ := time.Parse(time.RFC3339Nano, line.Timestamp)
	sid := line.SessionID
	if sid == "" {
		sid = fallbackSession
	}
	return AssistantReply{Timestamp: ts, SessionID: sid, Text: text}, true
}

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
	// String form (early Claude Code format).
	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// Array of typed parts (current format).
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
	return strings.TrimSpace(b.String())
}
