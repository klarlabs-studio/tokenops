package agentdx

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Source selects which client's transcripts to read.
type Source string

// Known sources. SourceAuto reads every client present on the machine,
// which is what an operator running more than one actually wants: the
// experience is theirs, not one tool's.
const (
	SourceAuto       Source = "auto"
	SourceClaudeCode Source = "claude-code"
	SourceCodex      Source = "codex"
	SourceCursor     Source = "cursor"
	SourceOpencode   Source = "opencode"
)

// ExtractOptions selects which transcripts to read.
type ExtractOptions struct {
	// Source picks the client. Empty means SourceAuto.
	Source Source
	// Root is the Claude Code projects directory. Empty resolves the
	// conventional ~/.claude/projects.
	Root string
	// Since drops entries older than this. Zero reads everything.
	Since time.Time
}

// DefaultRoot returns the conventional Claude Code projects directory.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agentdx: resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Extract walks the transcripts and flattens them into Records.
//
// Malformed lines are skipped rather than failing the scan: transcripts
// are appended live, so the final line of an active session is routinely
// half-written.
func Extract(opts ExtractOptions) ([]Record, error) {
	root := opts.Root
	if root == "" {
		r, err := DefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	files, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("agentdx: glob transcripts: %w", err)
	}
	var out []Record
	for _, path := range files {
		f, err := os.Open(path) //nolint:gosec // operator's own transcript dir
		if err != nil {
			continue
		}
		out = append(out, readTranscript(f, opts.Since)...)
		_ = f.Close()
	}
	return out, nil
}

// rawEntry is the subset of a transcript line these metrics need.
type rawEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	IsCompact bool   `json:"isCompactSummary"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens          int64 `json:"input_tokens"`
			CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
			CacheCreationTokens  int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// contentPart is one block of a structured message content array.
type contentPart struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Text  string `json:"text"`
	Input struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	} `json:"input"`
}

// interruptMarker is what the client writes into the transcript when the
// operator stops a turn.
const interruptMarker = "[request interrupted"

func readTranscript(r io.Reader, since time.Time) []Record {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	var out []Record
	for sc.Scan() {
		var e rawEntry
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
		base := Record{At: at, SessionID: e.SessionID}

		switch strings.ToLower(e.Type) {
		case "user":
			// The client writes the compaction summary as a user entry.
			// It is not an instruction the operator typed, so it must be
			// classified before the prompt check or it both under-counts
			// compactions and inflates the per-instruction denominator.
			if e.IsCompact {
				base.Kind = KindCompaction
				out = append(out, base)
				continue
			}
			parts, text := decodeContent(e.Message.Content)
			if strings.Contains(strings.ToLower(text), interruptMarker) {
				base.Kind = KindInterrupt
				out = append(out, base)
				continue
			}
			// Most user rows are tool results the agent fed back to
			// itself. Only a typed instruction opens a unit of work.
			if isOperatorPrompt(parts, text) {
				base.Kind = KindPrompt
				out = append(out, base)
			}
		case "assistant":
			turn := base
			turn.Kind = KindAssistantTurn
			turn.InputTokens = e.Message.Usage.InputTokens +
				e.Message.Usage.CacheReadInputTokens +
				e.Message.Usage.CacheCreationTokens
			out = append(out, turn)

			parts, _ := decodeContent(e.Message.Content)
			for _, p := range parts {
				if p.Type != "tool_use" {
					continue
				}
				tu := base
				tu.Kind = KindToolUse
				tu.ToolName = p.Name
				tu.FilePath = firstNonEmpty(p.Input.FilePath, p.Input.Path)
				out = append(out, tu)
			}
		}
	}
	return out
}

// decodeContent returns the structured parts and the concatenated text of
// a message's content, handling both the string and array shapes.
func decodeContent(raw json.RawMessage) ([]contentPart, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return nil, asString
	}
	var parts []contentPart
	if json.Unmarshal(raw, &parts) != nil {
		return nil, ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Text != "" {
			b.WriteString(p.Text)
			b.WriteByte(' ')
		}
	}
	return parts, strings.TrimSpace(b.String())
}

// isOperatorPrompt distinguishes a typed instruction from the tool
// results the client writes back under the same "user" type. In real
// sessions the echoes outnumber the prompts by roughly 44 to 1.
func isOperatorPrompt(parts []contentPart, text string) bool {
	if len(parts) == 0 {
		return strings.TrimSpace(text) != ""
	}
	for _, p := range parts {
		if p.Type != "tool_result" {
			return true
		}
	}
	return false
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// ExtractAll reads whichever clients the options select.
//
// Auto mode means "discover what is on this machine", which is inherently
// about default locations. A caller supplying an explicit Root is not
// discovering anything — they are pinning one tree — so a Root narrows
// auto to a single source rather than being ignored while the readers
// wander off to the real home directory. Without that rule a caller who
// pinned a directory would still get whatever the machine happened to
// hold, which is how a test ends up asserting against someone's real
// transcripts.
//
// A missing client is not an error. Most operators run one; reporting a
// failure because the other is absent would make the common case look
// broken.
func ExtractAll(opts ExtractOptions) ([]Record, error) {
	switch {
	case opts.Source == SourceClaudeCode:
		return Extract(opts)
	case opts.Source == SourceCodex:
		return ExtractCodex(opts)
	case opts.Source == SourceCursor:
		return ExtractCursor(opts)
	case opts.Source == SourceOpencode:
		return ExtractOpencode(opts)
	case opts.Root != "":
		// Pinned tree, unnamed source: read it as Claude Code, the
		// historical meaning of Root.
		return Extract(opts)
	}

	var out []Record
	if cc, err := Extract(opts); err == nil {
		out = append(out, cc...)
	}
	if cx, err := ExtractCodex(opts); err == nil {
		out = append(out, cx...)
	}
	// A Cursor schema failure is not a silent skip. Auto mode swallowing
	// it would put this reader back in the class of bugs it was written
	// to avoid: an unreadable store reported as an idle one.
	cu, err := ExtractCursor(opts)
	if err != nil {
		if errors.Is(err, ErrCursorSchema) {
			return out, err
		}
	} else {
		out = append(out, cu...)
	}
	oc, err := ExtractOpencode(opts)
	if err != nil {
		if errors.Is(err, ErrOpencodeSchema) {
			return out, err
		}
	} else {
		out = append(out, oc...)
	}
	return out, nil
}
