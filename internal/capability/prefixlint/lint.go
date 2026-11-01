package prefixlint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Client names.
const (
	ClientClaudeCode = "claude-code"
	ClientCodex      = "codex"
	ClientOpencode   = "opencode"
)

// maxFileBytes caps what is read of one file: a context file larger than
// this is not a context file anyone keeps.
const maxFileBytes = 1 << 20

// Lint reads src and reports every volatile item it finds: file items
// first, in the order the harness loads them, then hooks, then recorded
// hook output.
func Lint(src Sources) Report {
	l := &linter{src: src, seen: map[string]int{}}
	for _, f := range l.contextFiles() {
		l.file(f.path, f.clients, f.imports)
	}
	for _, path := range l.settingsFiles() {
		l.hooks(path)
	}
	if src.TranscriptRoot != "" {
		l.recorded()
	}
	r := Report{Findings: l.findings, Read: l.read, Transcripts: l.transcripts, Changes: []ChangeImpact{}}
	if l.changes != nil {
		r.Changes = l.changes.impacts()
	}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	if r.Read == nil {
		r.Read = []string{}
	}
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityHigh:
			r.High++
		case SeverityMedium:
			r.Medium++
		default:
			r.Low++
		}
	}
	return r
}

type linter struct {
	src         Sources
	findings    []Finding
	read        []string
	transcripts int
	// changes measures mid-session changes in the transcripts read.
	changes *changeBook
	// seen maps a read file to its index in read, so a file loaded by two
	// clients or imported twice is read once.
	seen map[string]int
	// hookPaths maps a configured hook command to where it is configured.
	hookPaths map[string]hookSite
}

type contextFile struct {
	path    string
	clients []string
	// imports says the file's @imports are followed (Claude Code only).
	imports bool
}

// contextFiles lists the always-loaded files, user files first, then the
// project's from the repository root down. A file two clients load is
// listed once with both.
func (l *linter) contextFiles() []contextFile {
	var out []contextFile
	addFile := func(path string, client string, imports bool) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for i := range out {
			if out[i].path == path {
				out[i].clients = appendUnique(out[i].clients, client)
				out[i].imports = out[i].imports || imports
				return
			}
		}
		out = append(out, contextFile{path: path, clients: []string{client}, imports: imports})
	}
	if l.src.ClaudeDir != "" {
		addFile(filepath.Join(l.src.ClaudeDir, "CLAUDE.md"), ClientClaudeCode, true)
	}
	if l.src.CodexDir != "" {
		addFile(filepath.Join(l.src.CodexDir, "AGENTS.md"), ClientCodex, false)
	}
	if l.src.OpencodeDir != "" {
		addFile(filepath.Join(l.src.OpencodeDir, "AGENTS.md"), ClientOpencode, false)
	}
	for _, dir := range projectDirs(l.src.ProjectDir) {
		addFile(filepath.Join(dir, "CLAUDE.md"), ClientClaudeCode, true)
		addFile(filepath.Join(dir, ".claude", "CLAUDE.md"), ClientClaudeCode, true)
		addFile(filepath.Join(dir, "CLAUDE.local.md"), ClientClaudeCode, true)
		addFile(filepath.Join(dir, "AGENTS.md"), ClientCodex, false)
		addFile(filepath.Join(dir, "AGENTS.md"), ClientOpencode, false)
	}
	return out
}

// projectDirs are dir and its parents up to the repository root (the
// first one holding .git), root first. Without a repository, dir alone.
func projectDirs(dir string) []string {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	var chain []string
	for d := dir; ; {
		chain = append(chain, d)
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			chain = []string{dir}
			break
		}
		d = parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// readOnce reads a regular file the first time it is asked for, and
// records it as read.
func (l *linter) readOnce(path string) (string, bool) {
	if _, done := l.seen[path]; done {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return "", false
	}
	b, err := os.ReadFile(path) //nolint:gosec // the harness's own context files, read only
	if err != nil {
		return "", false
	}
	l.seen[path] = len(l.read)
	l.read = append(l.read, path)
	return string(b), true
}

// file lints one context file and, when imports is set, the files it
// @imports, one level deep.
func (l *linter) file(path string, clients []string, imports bool) {
	text, ok := l.readOnce(path)
	if !ok {
		return
	}
	l.text(path, clients, text)
	if !imports {
		return
	}
	for _, imp := range importsOf(text, filepath.Dir(path), l.src.Home) {
		if body, ok := l.readOnce(imp); ok {
			l.text(imp, clients, body)
		}
	}
}

// text turns a file's matches into findings. A value on a line that says
// it is kept current ("Last updated: …") is rewritten by whatever keeps
// it, so it is medium; other values change only when the file is edited.
func (l *linter) text(path string, clients []string, text string) {
	matches := ScanText(text)
	marked := map[int]string{}
	values := map[int]bool{}
	for _, m := range matches {
		if isMarker(m.Kind) {
			if _, ok := marked[m.Line]; !ok {
				marked[m.Line] = m.Kind
			}
		} else {
			values[m.Line] = true
		}
	}
	for _, m := range matches {
		if isMarker(m.Kind) && values[m.Line] {
			continue // reported with its value
		}
		sev := SeverityLow
		if _, ok := marked[m.Line]; ok || m.Kind == KindCounter || isMarker(m.Kind) {
			sev = SeverityMedium
		}
		l.findings = append(l.findings, Finding{
			Severity: sev, Source: SourceFile, Clients: clients,
			Path: path, Line: m.Line, Kind: m.Kind, Token: truncate(m.Token),
			Why: fileWhy(sev, marked[m.Line]), Fix: fileFix(m.Kind),
		})
	}
}

func fileWhy(sev, marker string) string {
	if sev == SeverityMedium {
		if marker == "" {
			marker = "kept current"
		}
		return "This line is " + markerWords(marker) + ", so whatever keeps it current rewrites the always-loaded context. " +
			"Each rewrite changes the prompt from this line on, and the next sessions rebuild the cache from here instead of reusing it."
	}
	return "A value written into an always-loaded file changes only when the file is edited, but each edit changes the prompt from this line on, " +
		"so the cache built before the edit is not reused after this point."
}

func markerWords(marker string) string {
	switch marker {
	case KindToday:
		return "dated as today"
	case KindLastUpdated:
		return "a last-updated line"
	}
	return marker
}

func fileFix(kind string) string {
	switch kind {
	case KindTimestamp, KindDate, KindTime, KindToday, KindLastUpdated:
		return "Drop it, or move it to the end of the file, after the stable part; the agent can run `date` when it needs the date, and git keeps when the file changed."
	case KindCounter:
		return "Drop the counter from the always-loaded file; keep it in a file the agent reads when it needs it."
	default:
		return "Move it to the end of the file, after the stable part, or out of the always-loaded files."
	}
}

// importRef is an @import: "@path" at the start of a line or after a
// space. A word with an @ inside (an email) is not one.
var importRef = regexp.MustCompile(`(?:^|\s)@([~./A-Za-z0-9_][^\s` + "`" + `)\]}>,;]*)`)

// importsOf lists the files text @imports that exist, resolved against
// dir or home. Code spans and fenced blocks are skipped, as Claude Code
// skips them; a reference that is not a readable file (`@types/node`) is
// dropped.
func importsOf(text, dir, home string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		line = stripCodeSpans(line)
		for _, m := range importRef.FindAllStringSubmatch(line, -1) {
			ref := strings.TrimRight(m[1], ".:")
			var path string
			switch {
			case strings.HasPrefix(ref, "~/"):
				if home == "" {
					continue
				}
				path = filepath.Join(home, ref[2:])
			case filepath.IsAbs(ref):
				path = ref
			default:
				path = filepath.Join(dir, ref)
			}
			path = filepath.Clean(path)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				out = appendUnique(out, path)
			}
		}
	}
	return out
}

func stripCodeSpans(line string) string {
	var b strings.Builder
	in := false
	for _, r := range line {
		if r == '`' {
			in = !in
			b.WriteRune(' ')
			continue
		}
		if in {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncate keeps a token short; a token is a value, never a sentence.
func truncate(s string) string {
	const max = 48
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
