package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
)

// tokenops_rules (view=lint_context) answers the volatile-prefix lint
// for a project, from the same capability as `tokenops coach
// lint-context`.
func TestRulesLintContextView(t *testing.T) {
	srv := fullServer(t) // sets its own HOME, which this test replaces
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"git status"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("Use go 1.26 and v1.2.3.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := callPublic(t, srv, "tokenops_rules", map[string]any{"view": "lint_context", "root": project})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out["lint_context"])
	var r prefixlint.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if r.High != 1 || len(r.Findings) != 1 || r.Findings[0].Token != "git status" {
		t.Errorf("report = %s, want the git status hook only", raw)
	}
}

// TokenOps' own text in every session's prefix — the server instructions
// and the tool descriptions each client loads — carries nothing that
// changes between sessions.
func TestTokenOpsContextIsStable(t *testing.T) {
	if got := prefixlint.ScanText(instructions); len(got) != 0 {
		t.Errorf("server instructions carry volatile text: %+v", got)
	}
	for _, p := range publicTools() {
		if got := prefixlint.ScanText(p.description); len(got) != 0 {
			t.Errorf("%s description carries volatile text: %+v", p.name, got)
		}
	}
	if strings.TrimSpace(instructions) == "" {
		t.Fatal("no instructions")
	}
}
