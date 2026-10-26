package prefixlint

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Claude Code's transcripts record each hook that ran as an attachment
// line: {"type":"attachment","sessionId":…,"attachment":{"type":
// "hook_success","hookName":"SessionStart:startup","hookEvent":
// "SessionStart","command":…,"stdout":…}}. The context a hook added is
// its stdout: plain text, or JSON whose hookSpecificOutput carries
// additionalContext.

// recordedHook is one hook run's record, as much as the lint reads.
type recordedHook struct {
	SessionID  string `json:"sessionId"`
	Attachment struct {
		Type      string `json:"type"`
		HookName  string `json:"hookName"`
		HookEvent string `json:"hookEvent"`
		Command   string `json:"command"`
		Stdout    string `json:"stdout"`
	} `json:"attachment"`
}

// prefixHook says a recorded run's output went into the prefix: a
// SessionStart at startup or after /clear opens the context, while one on
// resume or compact is appended after it. UserPromptSubmit is kept too,
// at its own severity.
func prefixHook(name, event string) bool {
	switch event {
	case EventSessionStart:
		_, source, _ := strings.Cut(name, ":")
		return source == "" || source == "startup" || source == "clear"
	case EventUserPromptSubmit:
		return true
	}
	return false
}

// hookRuns are one hook command's recorded outputs.
type hookRuns struct {
	event, command string
	// bySession is the first output's digest in each session.
	bySession map[string][sha256.Size]byte
	// tokens are the volatile tokens seen in its output, first of each
	// kind.
	tokens []Match
}

// recorded reads the newest transcripts for what SessionStart and
// UserPromptSubmit hooks printed, and reports a hook whose output carries
// a volatile token or, for SessionStart, differs between sessions. Output
// is hashed and scanned; none of it is kept.
func (l *linter) recorded() {
	runs := map[string]*hookRuns{}
	for _, path := range newestTranscripts(l.src.TranscriptRoot, l.maxTranscripts()) {
		if readHookRuns(path, runs) {
			l.transcripts++
		}
	}
	ordered := make([]*hookRuns, 0, len(runs))
	for _, r := range runs {
		ordered = append(ordered, r)
	}
	// Configured hooks in the order they are configured, then the rest by
	// command.
	rank := func(r *hookRuns) (string, int, string) {
		if site, ok := l.hookPaths[r.command]; ok {
			return "0" + site.path, site.line, r.command
		}
		return "1", 0, r.command
	}
	sort.Slice(ordered, func(i, j int) bool {
		pi, li, ci := rank(ordered[i])
		pj, lj, cj := rank(ordered[j])
		if pi != pj {
			return pi < pj
		}
		if li != lj {
			return li < lj
		}
		return ci < cj
	})
	for _, r := range ordered {
		l.findings = append(l.findings, l.recordedFindings(r)...)
	}
}

func (l *linter) maxTranscripts() int {
	if l.src.MaxTranscripts > 0 {
		return l.src.MaxTranscripts
	}
	return DefaultMaxTranscripts
}

func (l *linter) recordedFindings(r *hookRuns) []Finding {
	site, configured := l.hookPaths[r.command]
	base := Finding{
		Source: SourceHookOutput, Clients: []string{ClientClaudeCode},
		Hook: r.event, Command: program(r.command),
	}
	if configured {
		base.Path, base.Line = site.path, site.line
	}
	sev, fix := SeverityHigh, "Drop the changing part from "+r.event+"'s output, or move it out of the hook: let the agent fetch it when it needs it."
	why := "Transcripts show this SessionStart hook printing %s into the context at the start of a session, before your first message; it differs each session, " +
		"so no session reuses another's cached prefix past this point."
	if r.event == EventUserPromptSubmit {
		sev = SeverityMedium
		why = "Transcripts show this UserPromptSubmit hook printing %s on a turn; each copy is new input never served from the cache."
	}
	var out []Finding
	for _, m := range r.tokens {
		f := base
		f.Severity, f.Kind, f.Token, f.Fix = sev, m.Kind, truncate(m.Token), fix
		f.Why = fmt.Sprintf(why, "a "+kindWords(m.Kind))
		out = append(out, f)
	}
	if len(out) == 0 && r.event == EventSessionStart && len(r.bySession) > 1 {
		distinct := map[[sha256.Size]byte]bool{}
		for _, d := range r.bySession {
			distinct[d] = true
		}
		if len(distinct) > 1 {
			f := base
			f.Severity, f.Kind, f.Fix = SeverityHigh, KindOutputVaries, fix
			f.Token = fmt.Sprintf("%d different outputs in %d sessions", len(distinct), len(r.bySession))
			f.Why = "Transcripts show this SessionStart hook printing different text in different sessions. Its output opens the context, " +
				"so each session's prefix differs from here and none reuses another's cache past it."
			out = append(out, f)
		}
	}
	return out
}

func kindWords(kind string) string {
	switch kind {
	case KindTimestamp:
		return "timestamp"
	case KindDate:
		return "date"
	case KindTime:
		return "clock time"
	case KindUUID:
		return "UUID"
	case KindSessionID:
		return "session id"
	case KindGitSHA:
		return "commit hash"
	case KindCounter:
		return "counter"
	}
	return "changing value"
}

// newestTranscripts lists up to max .jsonl files under root, newest
// first.
func newestTranscripts(root string, max int) []string {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable directory is skipped
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, file{path, info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > max {
		files = files[:max]
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

var hookSuccess = []byte(`"hook_success"`)

// readHookRuns adds one transcript's hook runs to runs; false when the
// file could not be read.
func readHookRuns(path string, runs map[string]*hookRuns) bool {
	f, err := os.Open(path) //nolint:gosec // transcripts under the client's projects directory
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if bytes.Contains(line, hookSuccess) {
			addRun(line, runs)
		}
		if err == io.EOF {
			return true
		}
		if err != nil {
			return true
		}
	}
}

func addRun(line []byte, runs map[string]*hookRuns) {
	var rec recordedHook
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	a := rec.Attachment
	if a.Type != "hook_success" || !prefixHook(a.HookName, a.HookEvent) || a.Command == "" {
		return
	}
	key := a.HookEvent + "\x00" + a.Command
	r := runs[key]
	if r == nil {
		r = &hookRuns{event: a.HookEvent, command: a.Command, bySession: map[string][sha256.Size]byte{}}
		runs[key] = r
	}
	out := contextOf(a.Stdout)
	if _, seen := r.bySession[rec.SessionID]; !seen {
		r.bySession[rec.SessionID] = sha256.Sum256([]byte(out))
	}
	for _, m := range ScanText(out) {
		if isMarker(m.Kind) || hasKind(r.tokens, m.Kind) {
			continue
		}
		r.tokens = append(r.tokens, m)
	}
}

func hasKind(ms []Match, kind string) bool {
	for _, m := range ms {
		if m.Kind == kind {
			return true
		}
	}
	return false
}

// contextOf is what a hook's stdout adds to the context: JSON output's
// additionalContext, or plain text as it is.
func contextOf(stdout string) string {
	s := strings.TrimSpace(stdout)
	if !strings.HasPrefix(s, "{") {
		return s
	}
	var j struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal([]byte(s), &j) != nil {
		return s
	}
	return j.HookSpecificOutput.AdditionalContext
}
