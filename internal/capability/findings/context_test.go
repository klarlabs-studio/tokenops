package findings

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
)

func contextOnly(r Report) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Kind == KindContext {
			out = append(out, f)
		}
	}
	return out
}

// A high-severity volatile item in the always-loaded context is one
// finding, keyed without figures, that names the hook and points at the
// lint; low and medium items stay in the lint.
func TestVolatileContextFinding(t *testing.T) {
	date := prefixlint.Finding{Severity: prefixlint.SeverityHigh, Source: prefixlint.SourceHook, Hook: "SessionStart",
		Path: "/home/u/.claude/settings.json", Line: 12, Command: "date", Kind: prefixlint.KindClock, Token: "date"}
	gitStatus := date
	gitStatus.Kind, gitStatus.Token, gitStatus.Command = prefixlint.KindGit, "git status", "git"
	low := prefixlint.Finding{Severity: prefixlint.SeverityLow, Source: prefixlint.SourceFile, Path: "/home/u/.claude/CLAUDE.md", Line: 3, Kind: prefixlint.KindDate, Token: "2026-01-02"}

	if got := contextOnly(Compute(Inputs{Context: &prefixlint.Report{Findings: []prefixlint.Finding{low}, Low: 1}})); len(got) != 0 {
		t.Errorf("low items only: %+v, want no finding", got)
	}
	if got := contextOnly(Compute(Inputs{})); len(got) != 0 {
		t.Errorf("no lint: %+v", got)
	}

	one := contextOnly(Compute(Inputs{Context: &prefixlint.Report{Findings: []prefixlint.Finding{date, low}, High: 1, Low: 1}}))
	if len(one) != 1 {
		t.Fatalf("findings = %+v, want one", one)
	}
	f := one[0]
	if f.Key != "context:volatile-prefix" || f.Level != LevelNotice {
		t.Errorf("finding = %+v, want key context:volatile-prefix at notice", f)
	}
	if !strings.Contains(f.Title, "SessionStart") || !strings.Contains(f.Title, "date") {
		t.Errorf("title = %q, want it to name the hook and the command", f.Title)
	}
	if !strings.Contains(f.Evidence, "settings.json:12") {
		t.Errorf("evidence = %q, want the file and line", f.Evidence)
	}
	if !strings.Contains(f.Action, "tokenops coach lint-context") {
		t.Errorf("action = %q, want the lint command", f.Action)
	}

	two := contextOnly(Compute(Inputs{Context: &prefixlint.Report{Findings: []prefixlint.Finding{date, gitStatus}, High: 2}}))
	if len(two) != 1 || !strings.Contains(two[0].Title, "2 ") || two[0].Key != f.Key {
		t.Errorf("two items: %+v, want one finding counting both under the same key", two)
	}
}
