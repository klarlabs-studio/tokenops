package prefixlint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestScanTextFindsVolatileTokens(t *testing.T) {
	cases := []struct {
		text  string
		kind  string
		token string
	}{
		{"Today is 2026-10-26.", KindToday, "Today is"},
		{"Today is 2026-10-26.", KindDate, "2026-10-26"},
		{"Generated at 2026-10-26T10:15:00Z by the brief", KindTimestamp, "2026-10-26T10:15:00Z"},
		{"Generated at 2026-10-26T10:15:00Z by the brief", KindLastUpdated, "Generated at"},
		{"Last updated: October 26, 2026", KindDate, "October 26, 2026"},
		{"brief from Mon Oct 26 10:15:00 CET 2026", KindTimestamp, "Mon Oct 26 10:15:00 CET 2026"},
		{"started 10:15:42 local", KindTime, "10:15:42"},
		{"session 3f2b8c1e-9a4d-4e6f-8b2a-1c3d5e7f9a0b resumed", KindUUID, "3f2b8c1e-9a4d-4e6f-8b2a-1c3d5e7f9a0b"},
		{"session_id: abc123def", KindSessionID, "abc123def"},
		{"HEAD is at commit 9fceb02d0ae598e95dc970b74767f19372d61af8", KindGitSHA, "9fceb02d0ae598e95dc970b74767f19372d61af8"},
		{"Session count: 42", KindCounter, "Session count: 42"},
	}
	for _, c := range cases {
		got := ScanText(c.text)
		found := false
		for _, m := range got {
			if m.Kind == c.kind && m.Token == c.token && m.Line == 1 {
				found = true
			}
		}
		if !found {
			t.Errorf("ScanText(%q) = %+v, want %s %q on line 1", c.text, got, c.kind, c.token)
		}
	}
}

// Versions, ports, colours, issue numbers and plain numbers do not change
// between sessions and are not flagged.
func TestScanTextIgnoresStableNumbers(t *testing.T) {
	stable := []string{
		"go 1.26",
		"Keep go.mod on Go 1.26.0.",
		"Requires v1.2.3 or semver 2.10.0-rc.1+build.5",
		"calver 2026.10.0 release",
		"ports: \"8080:8080\" and 3000:3000",
		"ratio 16:9",
		"colour #ffffff and #a1b2c3",
		"ADR 0004 sets the sequencing; see issue #12 and PR #290",
		"the number 1234567 and the word deadbeef",
		"Pro=45 is the baseline; p90 873k; compact before 600k",
		"merge with gh pr merge <n> --squash --admin",
		"use `@types/node` and email felix@example.com",
		"sha256 checksums are verified",
		"run 3 tests",
	}
	for _, s := range stable {
		if got := ScanText(s); len(got) != 0 {
			t.Errorf("ScanText(%q) = %+v, want nothing", s, got)
		}
	}
}

func TestScanTextReportsLines(t *testing.T) {
	got := ScanText("# Rules\n\nstable line\nupdated 2026-01-02\n")
	if len(got) != 1 || got[0].Line != 4 || got[0].Token != "2026-01-02" {
		t.Fatalf("got %+v, want the date on line 4", got)
	}
}

// fixture is a home and a project in temporary directories.
type fixture struct {
	home, project string
	src           Sources
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")
	mkdir(t, filepath.Join(project, ".git"))
	return fixture{home: home, project: project, src: Sources{
		Home:        home,
		ClaudeDir:   filepath.Join(home, ".claude"),
		CodexDir:    filepath.Join(home, ".codex"),
		OpencodeDir: filepath.Join(home, ".config", "opencode"),
		ProjectDir:  project,
	}}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findingsOf(r Report, kind string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func TestLintFlagsDatesInClaudeMDAsLow(t *testing.T) {
	fx := newFixture(t)
	userMD := filepath.Join(fx.src.ClaudeDir, "CLAUDE.md")
	write(t, userMD, "# Me\n\nPrefer Go 1.26 and v1.2.3.\nNotes as of 2026-10-26: keep it short.\n")
	write(t, filepath.Join(fx.project, "CLAUDE.md"), "# Repo\n\nUse go 1.26. Run make test.\n")

	r := Lint(fx.src)
	dates := findingsOf(r, KindDate)
	if len(dates) != 1 {
		t.Fatalf("date findings = %+v, want one", r.Findings)
	}
	f := dates[0]
	if f.Path != userMD || f.Line != 4 || f.Severity != SeverityLow || f.Source != SourceFile {
		t.Errorf("finding = %+v, want %s:4, low, file", f, userMD)
	}
	if !slices.Equal(f.Clients, []string{"claude-code"}) {
		t.Errorf("clients = %v", f.Clients)
	}
	if f.Why == "" || f.Fix == "" {
		t.Errorf("finding lacks why or fix: %+v", f)
	}
	if len(r.Findings) != 1 {
		t.Errorf("findings = %+v, want only the date", r.Findings)
	}
	if !slices.Contains(r.Read, userMD) || !slices.Contains(r.Read, filepath.Join(fx.project, "CLAUDE.md")) {
		t.Errorf("read = %v, want both CLAUDE.md files", r.Read)
	}
	if r.Low != 1 || r.High != 0 {
		t.Errorf("counts low=%d high=%d", r.Low, r.High)
	}
}

func TestLintNeverEchoesFileText(t *testing.T) {
	fx := newFixture(t)
	write(t, filepath.Join(fx.project, "CLAUDE.md"), "secret-ish words surround Today is 2026-10-26 and more private words\n")
	r := Lint(fx.src)
	if len(r.Findings) == 0 {
		t.Fatal("no findings")
	}
	b, _ := json.Marshal(r)
	for _, w := range []string{"secret-ish", "private words", "surround"} {
		if strings.Contains(string(b), w) {
			t.Errorf("report carries file text %q: %s", w, b)
		}
	}
}

func TestLintReadsAgentsMDForCodexAndOpencode(t *testing.T) {
	fx := newFixture(t)
	write(t, filepath.Join(fx.project, "AGENTS.md"), "# Agents\nLast updated: 2026-09-01\nGo 1.26\n")
	write(t, filepath.Join(fx.src.CodexDir, "AGENTS.md"), "run id 3f2b8c1e-9a4d-4e6f-8b2a-1c3d5e7f9a0b\n")
	write(t, filepath.Join(fx.src.OpencodeDir, "AGENTS.md"), "stable: use go 1.26\n")

	r := Lint(fx.src)
	dates := findingsOf(r, KindDate)
	if len(dates) != 1 {
		t.Fatalf("date findings = %+v", r.Findings)
	}
	if !slices.Equal(dates[0].Clients, []string{"codex", "opencode"}) {
		t.Errorf("project AGENTS.md clients = %v, want codex and opencode", dates[0].Clients)
	}
	// A "last updated" line is rewritten by whatever keeps it current.
	if dates[0].Severity != SeverityMedium {
		t.Errorf("a dated last-updated line is %s, want medium", dates[0].Severity)
	}
	uuids := findingsOf(r, KindUUID)
	if len(uuids) != 1 || uuids[0].Path != filepath.Join(fx.src.CodexDir, "AGENTS.md") || !slices.Equal(uuids[0].Clients, []string{"codex"}) {
		t.Errorf("uuid findings = %+v, want one in ~/.codex/AGENTS.md", uuids)
	}
	if !slices.Contains(r.Read, filepath.Join(fx.src.OpencodeDir, "AGENTS.md")) {
		t.Errorf("read = %v, want opencode's AGENTS.md", r.Read)
	}
}

// @imports are followed one level, relative to the importing file or the
// home directory; an import of an import is not read, and a cycle is read
// once.
func TestLintFollowsImportsOneLevel(t *testing.T) {
	fx := newFixture(t)
	write(t, filepath.Join(fx.project, "CLAUDE.md"),
		"# Repo\nSee @docs/notes.md and @~/shared.md.\nNot an import: `@docs/ignored.md`, felix@example.com, @types/node\n```\n@docs/fenced.md\n```\n")
	write(t, filepath.Join(fx.project, "docs", "notes.md"), "notes\nreviewed 2026-03-04\n@deeper.md\n@../CLAUDE.md\n")
	write(t, filepath.Join(fx.project, "docs", "deeper.md"), "deep 2026-05-06\n")
	write(t, filepath.Join(fx.project, "docs", "ignored.md"), "ignored 2026-07-08\n")
	write(t, filepath.Join(fx.project, "docs", "fenced.md"), "fenced 2026-07-09\n")
	write(t, filepath.Join(fx.home, "shared.md"), "shared 3f2b8c1e-9a4d-4e6f-8b2a-1c3d5e7f9a0b\n")

	r := Lint(fx.src)
	notes := filepath.Join(fx.project, "docs", "notes.md")
	var gotNotes, gotShared bool
	for _, f := range r.Findings {
		switch {
		case f.Path == notes && f.Token == "2026-03-04" && f.Line == 2:
			gotNotes = true
		case f.Path == filepath.Join(fx.home, "shared.md") && f.Kind == KindUUID:
			gotShared = true
		case strings.HasSuffix(f.Path, "deeper.md"), strings.HasSuffix(f.Path, "ignored.md"), strings.HasSuffix(f.Path, "fenced.md"):
			t.Errorf("read a file it should not have: %+v", f)
		}
	}
	if !gotNotes || !gotShared {
		t.Errorf("findings = %+v, want notes.md:2 and shared.md", r.Findings)
	}
	count := 0
	for _, p := range r.Read {
		if p == filepath.Join(fx.project, "CLAUDE.md") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("CLAUDE.md read %d times (cycle): %v", count, r.Read)
	}
}

func settingsWithHooks(t *testing.T, path string, hooks map[string][]string) {
	t.Helper()
	type cmd struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type group struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []cmd  `json:"hooks"`
	}
	out := map[string][]group{}
	for event, cmds := range hooks {
		g := group{}
		for _, c := range cmds {
			g.Hooks = append(g.Hooks, cmd{Type: "command", Command: c})
		}
		out[event] = []group{g}
	}
	b, err := json.MarshalIndent(map[string]any{"model": "x", "hooks": out}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(b))
}

func TestLintFlagsSessionStartHookThatPrintsTheDate(t *testing.T) {
	fx := newFixture(t)
	settings := filepath.Join(fx.src.ClaudeDir, "settings.json")
	settingsWithHooks(t, settings, map[string][]string{
		EventSessionStart: {
			"date",
			`echo "Today: $(date +%F)"`,
			"git status --short | head -20",
			"date > /tmp/last-session",
			"mnemos hook brief --update-format x",
			"git log -1 --format=%H 2>/dev/null",
			"bash -c 'uuidgen'",
		},
		EventUserPromptSubmit: {"date -u"},
		"PreToolUse":          {"date"},
	})

	r := Lint(fx.src)
	hooks := map[string][]Finding{}
	for _, f := range r.Findings {
		if f.Source == SourceHook {
			hooks[f.Hook] = append(hooks[f.Hook], f)
		}
	}
	ss := hooks[EventSessionStart]
	if len(ss) != 5 {
		t.Fatalf("SessionStart findings = %+v, want date, $(date), git status, git log, uuidgen under bash -c", ss)
	}
	for _, f := range ss {
		if f.Severity != SeverityHigh || f.Path != settings || f.Line == 0 || f.Fix == "" {
			t.Errorf("SessionStart finding = %+v, want high with path, line and fix", f)
		}
	}
	tokens := make([]string, 0, len(ss))
	for _, f := range ss {
		tokens = append(tokens, f.Token)
	}
	for _, want := range []string{"date", "git status", "git log", "uuidgen"} {
		if !slices.Contains(tokens, want) {
			t.Errorf("tokens = %v, want %q", tokens, want)
		}
	}
	if ss[0].Kind != KindClock || ss[0].Command != "date" {
		t.Errorf("first finding = %+v, want the clock command date", ss[0])
	}
	if !strings.Contains(ss[0].Fix, "date") {
		t.Errorf("fix = %q, want it to say let the agent run date", ss[0].Fix)
	}
	ups := hooks[EventUserPromptSubmit]
	if len(ups) != 1 || ups[0].Severity != SeverityMedium {
		t.Errorf("UserPromptSubmit findings = %+v, want one medium", ups)
	}
	if len(hooks["PreToolUse"]) != 0 {
		t.Errorf("PreToolUse is not in the context: %+v", hooks["PreToolUse"])
	}
	if r.High != 5 {
		t.Errorf("high = %d, want 5", r.High)
	}
}

func TestLintReadsProjectSettingsHooks(t *testing.T) {
	fx := newFixture(t)
	local := filepath.Join(fx.project, ".claude", "settings.local.json")
	settingsWithHooks(t, local, map[string][]string{EventSessionStart: {"uuidgen"}})
	r := Lint(fx.src)
	got := findingsOf(r, KindRandom)
	if len(got) != 1 || got[0].Path != local || got[0].Severity != SeverityHigh {
		t.Errorf("findings = %+v, want uuidgen in %s", r.Findings, local)
	}
}

// transcript writes one Claude Code transcript with the given hook
// records.
func transcript(t *testing.T, root, session string, records ...map[string]any) {
	t.Helper()
	lines := make([]string, 0, 1+len(records))
	lines = append(lines, `{"type":"user","sessionId":"`+session+`","message":{"content":"hi"}}`)
	for _, a := range records {
		b, err := json.Marshal(map[string]any{"type": "attachment", "sessionId": session, "attachment": a})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	write(t, filepath.Join(root, "proj", session+".jsonl"), strings.Join(lines, "\n")+"\n")
}

func hookRecord(name, command, stdout string) map[string]any {
	event, _, _ := strings.Cut(name, ":")
	return map[string]any{"type": "hook_success", "hookName": name, "hookEvent": event,
		"command": command, "stdout": stdout, "content": stdout, "exitCode": 0}
}

func TestLintReadsRecordedHookOutput(t *testing.T) {
	fx := newFixture(t)
	root := filepath.Join(fx.home, ".claude", "projects")
	fx.src.TranscriptRoot = root
	settings := filepath.Join(fx.src.ClaudeDir, "settings.json")
	settingsWithHooks(t, settings, map[string][]string{EventSessionStart: {"~/bin/brief.sh", "~/bin/varies.sh", "~/bin/same.sh"}})

	ctx := func(s string) string {
		b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "additionalContext": s}})
		return string(b)
	}
	transcript(t, root, "s1",
		hookRecord("SessionStart:startup", "~/bin/brief.sh", ctx("private brief text, generated 2026-10-25T09:00:00Z")),
		hookRecord("SessionStart:startup", "~/bin/varies.sh", "you have 3 open items"),
		hookRecord("SessionStart:startup", "~/bin/same.sh", "stable rules"),
		hookRecord("SessionStart:resume", "~/bin/resume.sh", "resumed 2026-10-25T09:00:00Z"),
		hookRecord("Stop", "~/bin/stop.sh", "stopped 2026-10-25T09:00:00Z"),
	)
	transcript(t, root, "s2",
		hookRecord("SessionStart:startup", "~/bin/varies.sh", "you have 4 open items"),
		hookRecord("SessionStart:startup", "~/bin/same.sh", "stable rules"),
	)

	r := Lint(fx.src)
	if r.Transcripts != 2 {
		t.Errorf("transcripts read = %d, want 2", r.Transcripts)
	}
	var out []Finding
	for _, f := range r.Findings {
		if f.Source == SourceHookOutput {
			out = append(out, f)
		}
	}
	if len(out) != 2 {
		t.Fatalf("hook-output findings = %+v, want the timestamp and the varying output", out)
	}
	ts, varies := out[0], out[1]
	if ts.Kind != KindTimestamp || ts.Token != "2026-10-25T09:00:00Z" || ts.Severity != SeverityHigh ||
		ts.Command != "brief.sh" || ts.Path != settings || ts.Hook != EventSessionStart {
		t.Errorf("timestamp finding = %+v", ts)
	}
	if varies.Kind != KindOutputVaries || varies.Severity != SeverityHigh || varies.Command != "varies.sh" {
		t.Errorf("varying-output finding = %+v", varies)
	}
	b, _ := json.Marshal(r)
	for _, w := range []string{"private brief", "open items", "stable rules"} {
		if strings.Contains(string(b), w) {
			t.Errorf("report carries hook output %q", w)
		}
	}
}

func TestLintWithNothingConfigured(t *testing.T) {
	fx := newFixture(t)
	r := Lint(fx.src)
	if r.Findings == nil || len(r.Findings) != 0 || r.Read == nil {
		t.Errorf("empty machine: %+v", r)
	}
}
