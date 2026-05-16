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
	"regexp"
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

// Source identifies which JSONL dialect to parse. The default
// SourceClaudeCode walks ~/.claude/projects/**/*.jsonl. SourceCodex
// walks ~/.codex/sessions/rollout-*.jsonl. SourceAuto scans both
// when present so an operator running Claude Code + Codex gets one
// unified prompt-coach report.
type Source string

const (
	SourceAuto       Source = ""
	SourceClaudeCode Source = "claude-code"
	SourceCodex      Source = "codex"
)

// ExtractOptions filters the walk. Zero values mean "no filter".
type ExtractOptions struct {
	Root      string    // base directory; defaults to ~/.claude/projects / ~/.codex/sessions per Source
	Source    Source    // which JSONL dialect; empty = auto-detect both
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

// Extract walks the appropriate JSONL tree, parses every file, and
// returns the user-typed prompts that pass the filters. Files
// unreadable for permission reasons are skipped (best-effort scanning
// beats failing the whole report). Parse errors are skipped per-line
// so one bad turn in a 15MB file doesn't kill the walk.
//
// When opts.Source is SourceAuto and opts.Root is empty, BOTH the
// Claude Code and Codex default roots are scanned and merged. This
// is the default behavior: a single `tokenops coach prompts` reports
// across every CLI the operator uses.
func Extract(opts ExtractOptions) ([]UserPrompt, error) {
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
		// Operator supplied a Root but no Source; sniff from the path.
		if strings.Contains(root, ".codex") {
			src = SourceCodex
		} else {
			src = SourceClaudeCode
		}
	}
	return extractFromRoot(root, src, opts)
}

// extractAuto unions Claude Code + Codex extracts so the default
// invocation surfaces every CLI the operator uses. Each tree may be
// absent (no permission, never installed) — Extract returns the
// union of what it could read.
func extractAuto(opts ExtractOptions) ([]UserPrompt, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var out []UserPrompt
	for _, candidate := range []struct {
		root string
		src  Source
	}{
		{filepath.Join(home, ".claude", "projects"), SourceClaudeCode},
		{filepath.Join(home, ".codex", "sessions"), SourceCodex},
	} {
		if _, statErr := os.Stat(candidate.root); statErr != nil {
			continue
		}
		got, err := extractFromRoot(candidate.root, candidate.src, opts)
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

// extractFromRoot dispatches to the source-specific scanner. Each
// scanner emits UserPrompts honouring SessionID + Limit + the time
// window.
func extractFromRoot(root string, src Source, opts ExtractOptions) ([]UserPrompt, error) {
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
		// Codex JSONLs don't always stamp per-line timestamps the way
		// Claude Code does — the rollout filename carries the session
		// timestamp. Capture it on first session-meta line as the
		// fallback for downstream Since/Until filtering.
		sessionFallbackTS := timestampFromCodexFilename(path)
		sessionFallbackID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		for scanner.Scan() {
			p, ok := parseLine(scanner.Bytes(), src, sessionFallbackTS, sessionFallbackID)
			if !ok {
				continue
			}
			if !opts.Since.IsZero() && p.Timestamp.Before(opts.Since) {
				continue
			}
			if !opts.Until.IsZero() && p.Timestamp.After(opts.Until) {
				continue
			}
			out = append(out, p)
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

// codexLine is the per-record shape for Codex JSONLs. Codex emits
// flat `{"role":"user","content":[{"type":"input_text","text":...}]}`
// records (no top-level `type` discriminator), so the parser needs a
// different schema than Claude Code's `{"type":"user","message":...}`.
type codexLine struct {
	Role      string            `json:"role"`
	Timestamp string            `json:"timestamp"`
	Content   []codexContentPrt `json:"content"`
}

type codexContentPrt struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// codexRolloutFilenameRE captures the timestamp embedded in
// `rollout-2025-07-12T15-05-18-<uuid>.jsonl`. Codex doesn't always
// stamp per-record timestamps, but the filename does — used as the
// fallback Timestamp when a record omits its own.
//
// Format: rollout-YYYY-MM-DDTHH-MM-SS-...
var codexRolloutFilenameRE = mustCompileRE(`rollout-(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})`)

// timestampFromCodexFilename parses the rollout filename's timestamp
// portion. Returns the zero time when the path doesn't match the
// Codex naming convention (Claude Code paths always do).
func timestampFromCodexFilename(path string) time.Time {
	m := codexRolloutFilenameRE.FindStringSubmatch(filepath.Base(path))
	if len(m) < 5 {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02T15:04:05", m[1]+"T"+m[2]+":"+m[3]+":"+m[4])
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// parseLine extracts one UserPrompt from a raw JSONL line, picking
// the per-source parser. Returns ok=false when the line is not a
// human user turn (assistant, tool result, synthetic system message)
// or fails to parse.
func parseLine(raw []byte, src Source, fallbackTS time.Time, fallbackSession string) (UserPrompt, bool) {
	switch src {
	case SourceCodex:
		var c codexLine
		if err := json.Unmarshal(raw, &c); err != nil || c.Role != "user" {
			return UserPrompt{}, false
		}
		var b strings.Builder
		for _, p := range c.Content {
			if p.Type == "input_text" || p.Type == "text" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			}
		}
		text := b.String()
		if text == "" || isSyntheticMeta(text) {
			return UserPrompt{}, false
		}
		ts := fallbackTS
		if c.Timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, c.Timestamp); err == nil {
				ts = parsed
			}
		}
		return UserPrompt{
			Timestamp: ts,
			SessionID: fallbackSession,
			Text:      text,
		}, true
	default:
		// Claude Code: top-level type=user, message.content.
		var line rawLine
		if err := json.Unmarshal(raw, &line); err != nil {
			return UserPrompt{}, false
		}
		if line.Type != "user" {
			return UserPrompt{}, false
		}
		ts, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil {
			return UserPrompt{}, false
		}
		text := decodeContent(line.Message)
		if text == "" || isSyntheticMeta(text) {
			return UserPrompt{}, false
		}
		return UserPrompt{
			Timestamp: ts,
			SessionID: line.SessionID,
			Text:      text,
		}, true
	}
}

// mustCompileRE compiles a regex at init time and panics on a bad
// pattern — these patterns are constants and the panic is a fail-
// loud signal that the build is broken.
func mustCompileRE(pat string) *regexp.Regexp {
	return regexp.MustCompile(pat)
}
