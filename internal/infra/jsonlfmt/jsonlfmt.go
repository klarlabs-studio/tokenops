// Package jsonlfmt makes the fmt learning loop self-wiring: it mines the
// Claude Code JSONL logs (~/.claude/projects) that Claude Code already
// writes, measures the content composition of what flows into context, and
// runs each Bash command's output through the deterministic formatter engine
// as a DRY RUN to estimate what `tokenops fmt` would save on the operator's
// real traffic — with zero setup, no daemon, and no commands wrapped.
//
// It reuses the same file-walking as the claude_code_jsonl vendor-usage
// poller and follows the same privacy stance as the coaching analyzers: tool
// output is read into memory to measure and compress, but never persisted —
// only sizes and per-command aggregates leave this package.
package jsonlfmt

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/fmtlearn"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/formatter"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
)

// Options tunes a scan.
type Options struct {
	// Root is the Claude Code projects dir; empty defaults to
	// ~/.claude/projects.
	Root string
	// MaxFiles caps how many (newest-first) session files are scanned; 0
	// scans all. The MCP path caps this for responsiveness; the CLI
	// defaults to all.
	MaxFiles int
}

// CommandROI is the dry-run compression result for one Bash command token
// across all its occurrences in the logs.
type CommandROI struct {
	Command         string `json:"command"`
	Runs            int    `json:"runs"`
	RawBytes        int64  `json:"raw_bytes"`
	SavedBalanced   int64  `json:"saved_balanced_bytes"`
	SavedAggressive int64  `json:"saved_aggressive_bytes"`
	// Handled reports whether a dedicated formatter recognised this
	// command (false = generic fallback → a next-formatter candidate).
	Handled bool `json:"handled"`
}

// Composition is the byte volume of context content by source.
type Composition struct {
	ByTool         map[string]int64 `json:"by_tool"`         // tool name -> tool_result bytes
	AssistantProse int64            `json:"assistant_prose"` // caveman's target
	UserProse      int64            `json:"user_prose"`
}

// Report is the full self-wiring analysis.
type Report struct {
	Root            string       `json:"root"`
	SessionsScanned int          `json:"sessions_scanned"`
	ToolResults     int          `json:"tool_results"`
	Composition     Composition  `json:"composition"`
	Commands        []CommandROI `json:"commands"` // Bash commands, by raw bytes desc
	TotalBashBytes  int64        `json:"total_bash_bytes"`
	SavedBalanced   int64        `json:"saved_balanced_bytes"`
	SavedAggressive int64        `json:"saved_aggressive_bytes"`
	GeneratedAtUnix int64        `json:"generated_at_unix"`
}

// EstTokens is the byte→token approximation used across fmt.
func EstTokens(b int64) int64 {
	if b <= 0 {
		return 0
	}
	return b / 4
}

type rawLine struct {
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Text      string          `json:"text,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// scanState carries the two fixed-level registries + accumulators through a
// scan so the registries are built once, not per tool_result.
type scanState struct {
	regBal  *formatter.Registry
	regAgg  *formatter.Registry
	rep     *Report
	roi     map[string]*CommandROI
	records []fmtlearn.Record
	now     time.Time
}

// Scan walks the JSONL logs and returns the composition + per-command
// dry-run ROI, plus fmtlearn compress-records synthesised from the real
// command history (so `fmt learn` reflects actual usage without any wrapped
// runs). formatters is the catalog to dry-run against (built-ins + user
// config); now is injected for deterministic record timestamps.
func Scan(formatters []formatter.Formatter, opts Options, now time.Time) (*Report, []fmtlearn.Record, error) {
	root := opts.Root
	if root == "" {
		r, err := claudecodejsonl.DefaultRoot()
		if err != nil {
			return nil, nil, err
		}
		root = r
	}
	files, err := findSessionFilesRecursive(root)
	if err != nil {
		return nil, nil, err
	}
	files = newestFirst(files)
	if opts.MaxFiles > 0 && len(files) > opts.MaxFiles {
		files = files[:opts.MaxFiles]
	}

	st := &scanState{
		regBal: formatter.NewRegistry(formatter.LossPolicy{Default: formatter.LossBalanced}, formatters...),
		regAgg: formatter.NewRegistry(formatter.LossPolicy{Default: formatter.LossAggressive}, formatters...),
		rep: &Report{
			Root:            root,
			Composition:     Composition{ByTool: map[string]int64{}},
			GeneratedAtUnix: now.Unix(),
		},
		roi: map[string]*CommandROI{},
		now: now,
	}

	for _, fp := range files {
		if scanFile(fp, st) {
			st.rep.SessionsScanned++
		}
	}

	for _, r := range st.roi {
		st.rep.Commands = append(st.rep.Commands, *r)
		st.rep.TotalBashBytes += r.RawBytes
		st.rep.SavedBalanced += r.SavedBalanced
		st.rep.SavedAggressive += r.SavedAggressive
	}
	sort.Slice(st.rep.Commands, func(i, j int) bool {
		if st.rep.Commands[i].RawBytes != st.rep.Commands[j].RawBytes {
			return st.rep.Commands[i].RawBytes > st.rep.Commands[j].RawBytes
		}
		return st.rep.Commands[i].Command < st.rep.Commands[j].Command
	})
	return st.rep, st.records, nil
}

// scanFile streams one JSONL file. Returns true if the file parsed at all.
func scanFile(path string, st *scanState) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	idCommand := map[string]string{} // tool_use_id -> Bash command token
	idName := map[string]string{}    // tool_use_id -> tool name
	// bufio.Reader (not Scanner) so a single huge JSONL line — common when a
	// large tool result is inlined — never truncates the file. Scanner's
	// token cap would silently skip exactly the big outputs fmt cares about.
	br := bufio.NewReaderSize(f, 1<<20)
	parsed := false

	for {
		line, err := readLongLine(br)
		if len(line) == 0 && err != nil {
			break
		}
		if len(line) == 0 {
			if err != nil {
				break
			}
			continue
		}
		var rl rawLine
		if err := json.Unmarshal(line, &rl); err != nil {
			continue
		}
		if len(rl.Message.Content) == 0 || rl.Message.Content[0] != '[' {
			continue
		}
		var blocks []rawBlock
		if err := json.Unmarshal(rl.Message.Content, &blocks); err != nil {
			continue
		}
		parsed = true
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				idName[b.ID] = b.Name
				if b.Name == "Bash" {
					idCommand[b.ID] = bashCommandToken(b.Input)
				}
			case "tool_result":
				name := idName[b.ToolUseID]
				if name == "" {
					name = "unknown"
				}
				out := blockContent(b.Content)
				st.rep.ToolResults++
				st.rep.Composition.ByTool[name] += int64(len(out))
				if name == "Bash" {
					accumulateBashROI(st, idCommand[b.ToolUseID], out)
				}
			case "text":
				if rl.Message.Role == "assistant" {
					st.rep.Composition.AssistantProse += int64(len(b.Text))
				} else {
					st.rep.Composition.UserProse += int64(len(b.Text))
				}
			}
		}
	}
	return parsed
}

// accumulateBashROI runs one Bash output through the balanced + aggressive
// registries and folds the savings into the per-command ROI + a fmtlearn
// record. The raw output is never retained.
func accumulateBashROI(st *scanState, command string, out []byte) {
	if command == "" {
		command = "unknown"
	}
	if len(out) == 0 {
		return
	}
	bal, handled := st.regBal.Format([]string{command}, out)
	agg, _ := st.regAgg.Format([]string{command}, out)

	r := st.roi[command]
	if r == nil {
		r = &CommandROI{Command: command, Handled: handled}
		st.roi[command] = r
	}
	r.Runs++
	r.RawBytes += int64(len(out))
	if bal.CriticalKept && bal.BytesAfter < bal.BytesBefore {
		r.SavedBalanced += int64(bal.BytesBefore - bal.BytesAfter)
	}
	if agg.CriticalKept && agg.BytesAfter < agg.BytesBefore {
		r.SavedAggressive += int64(agg.BytesBefore - agg.BytesAfter)
	}

	st.records = append(st.records, fmtlearn.Record{
		Type:            fmtlearn.RecordCompress,
		Command:         command,
		RawBytes:        int64(len(out)),
		CompactBytes:    int64(bal.BytesAfter),
		TokensSaved:     EstTokens(int64(bal.BytesBefore - bal.BytesAfter)),
		Handled:         handled,
		GenericFallback: !handled,
		CriticalKept:    bal.CriticalKept,
		TS:              st.now.UTC(),
	})
}

// findSessionFilesRecursive walks root for every *.jsonl file at any depth.
// Claude Code's layout is usually one level deep, but worktrees and nested
// checkouts create deeper paths; a recursive walk is the only way to see all
// of a user's real sessions (the depth-1 glob silently missed most of them).
func findSessionFilesRecursive(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs rather than abort the whole scan
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readLongLine reads one newline-terminated line with no size cap (bufio
// Scanner's token limit would truncate files with a large inlined tool
// result). Returns the line without its trailing CR/LF plus the read error
// (io.EOF on the final line).
func readLongLine(br *bufio.Reader) ([]byte, error) {
	b, err := br.ReadBytes('\n')
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b, err
}

// bashCommandToken extracts the leading command token from a Bash tool_use
// input ({"command":"git status ..."}). Returns "" when unavailable.
func bashCommandToken(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var probe struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &probe); err != nil {
		return ""
	}
	cmd := strings.TrimSpace(probe.Command)
	if cmd == "" {
		return ""
	}
	// Agents constantly prefix with `cd /path && <real command>` (and use
	// `;`, newlines to chain). The output belongs to the real command, not
	// the no-output prefix, so split the chain and take the first segment
	// that actually produces output — skipping cd/export/source/set and
	// bare env assignments. Without this, `cd X && go test` misattributes
	// all the go-test output to `cd`.
	for _, seg := range splitChain(cmd) {
		fields := strings.Fields(seg)
		i := 0
		for i < len(fields) && (fields[i] == "sudo" || strings.Contains(fields[i], "=")) {
			i++
		}
		if i >= len(fields) {
			continue
		}
		tok := fields[i]
		if j := strings.LastIndexAny(tok, "/\\"); j >= 0 {
			tok = tok[j+1:]
		}
		switch tok {
		case "cd", "export", "source", "set", "unset", "pushd", "popd", "":
			continue // no-output prefixes; look at the next segment
		}
		return tok
	}
	return ""
}

// splitChain breaks a shell command on top-level sequencing operators
// (&& || ; and newlines) into segments. It does not split on pipes — the
// output of `a | b` is b's, but attributing to the first stage keeps the
// output-shape heuristic simple and is corrected by the formatter's own
// content detection.
func splitChain(cmd string) []string {
	repl := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n")
	parts := strings.Split(repl.Replace(cmd), "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// blockContent renders a tool_result content field (string or array of
// {type:text,text}) to raw bytes.
func blockContent(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s)
	}
	var parts []rawBlock
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				b.WriteString(p.Text)
			} else if len(p.Content) > 0 {
				b.Write(blockContent(p.Content))
			}
		}
		return []byte(b.String())
	}
	return nil
}

// newestFirst sorts paths by mtime descending so a MaxFiles cap keeps the
// most recent sessions.
func newestFirst(files []string) []string {
	type fi struct {
		path string
		mod  time.Time
	}
	fis := make([]fi, 0, len(files))
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		fis = append(fis, fi{f, st.ModTime()})
	}
	sort.Slice(fis, func(i, j int) bool { return fis[i].mod.After(fis[j].mod) })
	out := make([]string, len(fis))
	for i, f := range fis {
		out[i] = f.path
	}
	return out
}
