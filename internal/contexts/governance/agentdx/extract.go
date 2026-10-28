package agentdx

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"go.klarlabs.de/tokenops/internal/infra/claudehome"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/scanscope"
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
	// WithPromptText carries each instruction's text into Record.Text.
	// Off by default: the metrics never needed the words, and a scan
	// that does not carry them cannot leak them. The narrative surfaces
	// opt in explicitly.
	WithPromptText bool

	// IncludeScratch keeps sessions run in throwaway directories — a
	// benchmark harness, a temporary clone, anything under /tmp.
	//
	// They are excluded by default because these metrics claim to
	// describe how the operator works, and a simulated session has no
	// operator. On one real machine leaving them in made 94% of a 7-day
	// window synthetic and graded it: 2 turns per instruction against
	// the operator's real 20, and every dimension an A.
	IncludeScratch bool

	// Cache, when set, keeps what each Claude Code and Codex transcript
	// yielded between scans, so a repeated scan reads only what changed.
	// It is not used when WithPromptText is set.
	Cache *TranscriptCache
}

// cache returns the cache this scan may use: none when it carries prompt
// text, which must not outlive the scan.
func (o ExtractOptions) cache() *TranscriptCache {
	if o.WithPromptText {
		return nil
	}
	return o.Cache
}

// DefaultRoot returns the conventional Claude Code projects directory.
func DefaultRoot() (string, error) {
	return claudehome.Join("projects"), nil
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
	if !opts.IncludeScratch {
		files = scanscope.Keep(files)
	}
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
			out = append(out, cache.claudeCode(path, info, opts.Since)...)
			continue
		}
		f, err := os.Open(path) //nolint:gosec // operator's own transcript dir
		if err != nil {
			continue
		}
		out = append(out, readTranscript(f, opts.Since, opts.WithPromptText)...)
		_ = f.Close()
	}
	if cache != nil {
		cache.retain(sourceClaudeCode, kept)
	}
	return out, nil
}

// rawEntry is the subset of a transcript line these metrics need.
type rawEntry struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	IsCompact bool   `json:"isCompactSummary"`
	// IsSidechain marks an entry a subagent wrote.
	IsSidechain bool   `json:"isSidechain"`
	RequestID   string `json:"requestId"`
	// DurationMs is set on a system turn_duration entry: the harness's
	// own measure of the turn that just ended.
	DurationMs int64 `json:"durationMs"`
	// Effort is the session's effort level when this entry was written;
	// Claude Code stamps it on assistant entries.
	Effort  string `json:"effort"`
	Message struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
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
	Type string `json:"type"`
	Name string `json:"name"`
	Text string `json:"text"`
	// ID is a tool_use block's id; ToolUseID is the tool_result's
	// reference back to it.
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
	Input     struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	} `json:"input"`
	// RawInput keeps the arguments verbatim so an identical call can be
	// recognised, which the typed fields above cannot do.
	RawInput json.RawMessage `json:"-"`
}

// rawToolUse mirrors contentPart for the second decode pass that keeps
// tool arguments intact.
type rawToolUse struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// callSignature identifies a tool call by name and arguments. Truncated
// because a signature only has to be distinctive, not complete, and some
// tool inputs run to megabytes.
func callSignature(name string, input json.RawMessage) string {
	if name == "" {
		return ""
	}
	arg := string(input)
	if len(arg) > 400 {
		arg = arg[:400]
	}
	return name + "|" + arg
}

// interruptMarker is what the client writes into the transcript when the
// operator stops a turn.
const interruptMarker = "[request interrupted"

// maxTranscriptLine is the longest transcript line read. A longer one
// ends the file's scan, as bufio.Scanner's limit does.
const maxTranscriptLine = 8 << 20

func readTranscript(r io.Reader, since time.Time, withText bool) []Record {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxTranscriptLine)
	var out []Record
	for sc.Scan() {
		out = appendTranscriptLine(out, sc.Bytes(), since, withText)
	}
	return out
}

// appendTranscriptLine appends what one transcript line yields. Each line
// stands alone, so a file read in pieces yields what it yields read whole.
func appendTranscriptLine(out []Record, line []byte, since time.Time, withText bool) []Record {
	var e rawEntry
	if json.Unmarshal(line, &e) != nil {
		return out
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return out
	}
	at = at.UTC()
	if !since.IsZero() && at.Before(since) {
		return out
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
			return append(out, base)
		}
		parts, text := decodeContent(e.Message.Content)
		if strings.Contains(strings.ToLower(text), interruptMarker) {
			base.Kind = KindInterrupt
			return append(out, base)
		}
		// Most user rows are tool results the agent fed back to
		// itself. Only a typed instruction opens a unit of work.
		if isOperatorPrompt(parts, text) {
			if withText {
				base.Text = text
			}
			base.Kind = KindPrompt
			base.Rejects = IsRejection(text)
			if e.IsSidechain {
				base.Role = RoleSubagent
			}
			return append(out, base)
		}
		// A result's arrival closes the span its call opened; the
		// pairing is by id, so parallel calls each get their own span.
		for _, p := range parts {
			if p.Type == "tool_result" && p.ToolUseID != "" {
				res := base
				res.Kind = KindToolResult
				res.CallID = p.ToolUseID
				out = append(out, res)
			}
		}
	case "system":
		if e.Subtype == "turn_duration" {
			end := base
			end.Kind = KindTurnEnd
			end.DurationMs = e.DurationMs
			out = append(out, end)
		}
	case "assistant":
		turn := base
		turn.Kind = KindAssistantTurn
		turn.RequestID = e.RequestID
		turn.Model = e.Message.Model
		turn.Effort = normEffort(e.Effort)
		turn.InputTokens = e.Message.Usage.InputTokens +
			e.Message.Usage.CacheReadInputTokens +
			e.Message.Usage.CacheCreationTokens
		out = append(out, turn)

		parts, _ := decodeContent(e.Message.Content)
		var raws []rawToolUse
		_ = json.Unmarshal(e.Message.Content, &raws)
		for i, p := range parts {
			if p.Type != "tool_use" {
				continue
			}
			tu := base
			tu.Kind = KindToolUse
			tu.ToolName = p.Name
			tu.CallID = p.ID
			tu.FilePath = firstNonEmpty(p.Input.FilePath, p.Input.Path)
			if i < len(raws) {
				tu.CallSignature = callSignature(p.Name, raws[i].Input)
			}
			out = append(out, tu)
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
	if isInjected(text) {
		return false
	}
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

// injectedPrefixes open a user-role entry the harness wrote, not one the
// operator typed: a slash command being expanded, a background task
// reporting back, a reminder injected into the turn.
//
// They arrive in the transcript as user messages because that is the only
// role available to them, so nothing but their shape distinguishes them
// from an instruction. Counting them inflates every per-instruction
// figure and puts "<task-notification>" in a story where a title should
// be.
var injectedPrefixes = []string{
	"<task-notification>",
	"<command-name>",
	"<system-reminder>",
	"<local-command-stdout>",
	"<local-command-stderr>",
	"caveat: the messages below were generated by the user while running",
}

// isInjected reports whether a user-role entry was written by the harness
// rather than typed by the operator.
// IsInjected reports whether a user-role entry's text was written by the
// harness (a slash command, a task notification, a reminder) rather than
// typed by the operator.
func IsInjected(text string) bool { return isInjected(text) }

func isInjected(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(t, p) {
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
		return stamped(SourceClaudeCode)(Extract(opts))
	case opts.Source == SourceCodex:
		return stamped(SourceCodex)(ExtractCodex(opts))
	case opts.Source == SourceCursor:
		return stamped(SourceCursor)(ExtractCursor(opts))
	case opts.Source == SourceOpencode:
		return stamped(SourceOpencode)(ExtractOpencode(opts))
	case opts.Root != "":
		// Pinned tree, unnamed source: read it as Claude Code, the
		// historical meaning of Root.
		return stamped(SourceClaudeCode)(Extract(opts))
	}

	var out []Record
	if cc, err := stamped(SourceClaudeCode)(Extract(opts)); err == nil {
		out = append(out, cc...)
	}
	if cx, err := stamped(SourceCodex)(ExtractCodex(opts)); err == nil {
		out = append(out, cx...)
	}
	// A schema failure is not a silent skip. Auto mode swallowing it would
	// put these readers back in the class of bugs they were written to
	// avoid: an unreadable store reported as an idle one. Nor does it stop
	// the others: every source that can be read still is, and the error
	// names each one that could not.
	var errs []error
	cu, err := stamped(SourceCursor)(ExtractCursor(opts))
	switch {
	case err == nil:
		out = append(out, cu...)
	case errors.Is(err, ErrCursorSchema), errors.Is(err, ErrNoCursorStore):
		errs = append(errs, err)
	}
	oc, err := stamped(SourceOpencode)(ExtractOpencode(opts))
	switch {
	case err == nil:
		out = append(out, oc...)
	case errors.Is(err, ErrOpencodeSchema), errors.Is(err, ErrNoOpencodeStore):
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// stamped marks every record a reader returned with the harness that
// wrote it, so a unit can say which client it came from. The records are
// the reader's own (a cached read hands out a copy), so stamping them in
// place changes nothing another scan holds.
func stamped(h Source) func([]Record, error) ([]Record, error) {
	return func(recs []Record, err error) ([]Record, error) {
		for i := range recs {
			recs[i].Harness = h
		}
		return recs, err
	}
}

// rejectionMarkers are short, unambiguous ways an operator says the last
// answer was wrong. Deliberately narrow: a false positive here inflates
// the one signal that resists the session-position confound, so the cost
// of over-matching is higher than the cost of missing a rephrasing.
var rejectionMarkers = []string{
	"no,", "nope", "wrong", "try again", "that's not", "thats not",
	"incorrect", "revert", "undo", "not what i", "still broken",
	"doesn't work", "doesnt work", "redo", "no that",
}

// IsRejection reports whether an instruction rejects what preceded it.
func IsRejection(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	for _, m := range rejectionMarkers {
		if strings.Contains(t, m) {
			return true
		}
	}
	return false
}

// normEffort canonicalises an effort level for grouping.
func normEffort(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
