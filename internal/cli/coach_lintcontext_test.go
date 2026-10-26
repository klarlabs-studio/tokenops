package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
)

// lintContextHome is a temporary home whose Claude Code settings run
// `date` at SessionStart, and a project whose CLAUDE.md carries a date
// beside a version number.
func lintContextHome(t *testing.T) (home, project string) {
	t.Helper()
	home = t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(k, home)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	project = filepath.Join(home, "work", "repo")
	files := map[string]string{
		filepath.Join(home, ".claude", "settings.json"): `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"date"}]}]}}`,
		filepath.Join(project, "CLAUDE.md"):             "# Repo\nUse go 1.26.\nReviewed 2026-10-01 by the team\n",
		filepath.Join(project, ".git", "HEAD"):          "ref: refs/heads/main\n",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, project
}

func TestCoachLintContextJSON(t *testing.T) {
	_, project := lintContextHome(t)
	out, err := executeRoot(t, "coach", "lint-context", "--root", project, "--json")
	if err != nil {
		t.Fatalf("lint-context: %v\n%s", err, out)
	}
	var r prefixlint.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if r.High != 1 || r.Low != 1 || len(r.Findings) != 2 {
		t.Fatalf("report = %+v, want the date hook (high) and the CLAUDE.md date (low)", r)
	}
	if r.Findings[0].Token != "2026-10-01" || r.Findings[1].Token != "date" || r.Findings[1].Hook != "SessionStart" {
		t.Errorf("findings = %+v", r.Findings)
	}
	if strings.Contains(out, "Reviewed") || strings.Contains(out, "the team") {
		t.Errorf("output carries file text:\n%s", out)
	}
}

func TestCoachLintContextText(t *testing.T) {
	_, project := lintContextHome(t)
	out, err := executeRoot(t, "coach", "lint-context", "--root", project)
	if err != nil {
		t.Fatalf("lint-context: %v\n%s", err, out)
	}
	for _, want := range []string{
		"1 high", "SessionStart", "`date`", "let the agent run `date`",
		filepath.Join(project, "CLAUDE.md") + ":3", "2026-10-01",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Reviewed") {
		t.Errorf("output carries file text:\n%s", out)
	}
}
