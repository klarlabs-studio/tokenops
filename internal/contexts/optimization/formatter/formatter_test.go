package formatter

import (
	"fmt"
	"strings"
	"testing"
)

// gitStatusRaw is a representative `git status` long output with noise.
const gitStatusRaw = `On branch main
Your branch is up to date with 'origin/main'.

Changes to be committed:
  (use "git restore --staged <file>..." to unstage)
	modified:   internal/proxy/server.go
	new file:   internal/proxy/routing.go

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
  (use "git restore <file>..." to discard changes in working directory)
	modified:   README.md

Untracked files:
  (use "git add <file>..." to include in what will be committed)
	scratch/a.txt
	scratch/b.txt
	scratch/c.txt

no changes added to commit (use "git add" and/or "git commit -a")
`

// criticalLinesIn returns the trimmed critical lines a formatter sees in s.
func criticalLinesIn(f Formatter, s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		t := strings.TrimSpace(strings.TrimRight(l, " \t\r"))
		if f.CriticalLine(t) {
			out = append(out, t)
		}
	}
	return out
}

func TestGit_CriticalLinesSurviveEveryLevel(t *testing.T) {
	g := NewGit()
	wantCritical := criticalLinesIn(g, gitStatusRaw)
	if len(wantCritical) == 0 {
		t.Fatal("fixture has no critical lines; test is meaningless")
	}

	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := g.Format([]byte(gitStatusRaw), level)
		if !ok {
			t.Fatalf("level=%s: Format returned ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s: CriticalKept=false, compact=%q", level, res.Compact)
		}
		compact := string(res.Compact)
		for _, crit := range wantCritical {
			if !strings.Contains(compact, crit) {
				t.Errorf("level=%s: critical line dropped: %q\ncompact:\n%s", level, crit, compact)
			}
		}
	}
}

func TestGit_LevelsReduceMonotonically(t *testing.T) {
	g := NewGit()
	sizes := map[LossLevel]int{}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := g.Format([]byte(gitStatusRaw), level)
		sizes[level] = res.BytesAfter
	}
	if sizes[LossConservative] < sizes[LossBalanced] {
		t.Errorf("conservative (%d) should not be smaller than balanced (%d)",
			sizes[LossConservative], sizes[LossBalanced])
	}
	if sizes[LossBalanced] < sizes[LossAggressive] {
		t.Errorf("balanced (%d) should not be smaller than aggressive (%d)",
			sizes[LossBalanced], sizes[LossAggressive])
	}
	// Balanced must actually save something on this noisy fixture.
	if sizes[LossBalanced] >= len(gitStatusRaw) {
		t.Errorf("balanced saved nothing: %d >= %d", sizes[LossBalanced], len(gitStatusRaw))
	}
}

func TestGit_AggressiveCollapsesUntracked(t *testing.T) {
	g := NewGit()
	// A large untracked block: the count summary is smaller than the
	// listing, so aggressive collapses it.
	var b strings.Builder
	b.WriteString("On branch main\n\nUntracked files:\n  (use \"git add <file>...\" to include)\n")
	for i := range 40 {
		fmt.Fprintf(&b, "\tsome/long/path/to/generated/file_%02d.txt\n", i)
	}
	b.WriteString("\nnothing added to commit\n")
	res, _ := g.Format([]byte(b.String()), LossAggressive)
	compact := string(res.Compact)
	if !strings.Contains(compact, "untracked files") {
		t.Errorf("aggressive should collapse large untracked block to a count; got:\n%s", compact)
	}
	if strings.Contains(compact, "file_20.txt") {
		t.Errorf("aggressive should drop individual untracked entries; got:\n%s", compact)
	}
}

func TestFormat_Deterministic(t *testing.T) {
	g := NewGit()
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		r1, _ := g.Format([]byte(gitStatusRaw), level)
		r2, _ := g.Format([]byte(gitStatusRaw), level)
		if string(r1.Compact) != string(r2.Compact) {
			t.Errorf("level=%s: Format is not deterministic", level)
		}
	}
}

func TestGit_NonStatusFallsBackToGeneric(t *testing.T) {
	g := NewGit()
	raw := "commit abc123\nAuthor: x\n\n\n    some log body\n"
	res, ok := g.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback note, got %q", res.Notes)
	}
}

func TestGeneric_DropsDuplicatesAndBlanks(t *testing.T) {
	raw := "line\nline\n\n\n\nother\n"
	res, ok := generic.Format([]byte(raw), LossConservative)
	if !ok {
		t.Fatal("ok=false")
	}
	compact := string(res.Compact)
	if strings.Count(compact, "line") != 1 {
		t.Errorf("duplicate consecutive line not collapsed: %q", compact)
	}
	if strings.Contains(compact, "\n\n\n") {
		t.Errorf("blank run not collapsed: %q", compact)
	}
}

func TestGeneric_StripsANSI(t *testing.T) {
	raw := "\x1b[31merror\x1b[0m here\n"
	res, _ := generic.Format([]byte(raw), LossConservative)
	if strings.Contains(string(res.Compact), "\x1b") {
		t.Errorf("ANSI escape not stripped: %q", res.Compact)
	}
}

func TestRegistry_DispatchAndFallback(t *testing.T) {
	reg := NewRegistry(
		LossPolicy{Default: LossBalanced, Overrides: map[string]LossLevel{"git": LossAggressive}},
		NewGit(),
	)
	// Known command dispatches to the git formatter (branch chatter and
	// hints dropped), path-prefixed argv[0] still keys as "git".
	res, handled := reg.Format([]string{"/usr/bin/git", "status"}, []byte(gitStatusRaw))
	if !handled {
		t.Error("git should be handled by a command formatter")
	}
	if strings.Contains(string(res.Compact), "On branch main") {
		t.Errorf("git formatter not applied (branch chatter should be dropped):\n%s", res.Compact)
	}
	if !res.CriticalKept {
		t.Error("git dispatch lost critical lines")
	}
	// Unknown command → generic fallback, handled=false.
	_, handled = reg.Format([]string{"mycmd"}, []byte("a\na\n\n\nb\n"))
	if handled {
		t.Error("unknown command should report handled=false (generic fallback)")
	}
}

func TestLossPolicy_LevelFor(t *testing.T) {
	p := LossPolicy{Default: LossConservative, Overrides: map[string]LossLevel{"docker": LossAggressive}}
	if p.LevelFor("docker") != LossAggressive {
		t.Error("override not honoured")
	}
	if p.LevelFor("git") != LossConservative {
		t.Error("default not applied")
	}
}

func TestParseLossLevel(t *testing.T) {
	cases := map[string]LossLevel{
		"conservative": LossConservative,
		"balanced":     LossBalanced,
		"aggressive":   LossAggressive,
		"":             LossConservative,
	}
	for in, want := range cases {
		got, ok := ParseLossLevel(in)
		if !ok || got != want {
			t.Errorf("ParseLossLevel(%q) = %v, ok=%v; want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseLossLevel("bogus"); ok {
		t.Error("bogus level should report ok=false")
	}
}
