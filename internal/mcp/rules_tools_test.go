package mcp

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMCPRuleCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"),
		[]byte("# Testing\nuse tdd\n## Style\nbe concise\n## Other\nexplain thoroughly with detailed reasoning\n"),
		0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"),
		[]byte("# Testing\nuse tdd\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func newRulesServer(t *testing.T) *Server {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterRulesTools(srv); err != nil {
		t.Fatalf("RegisterRulesTools: %v", err)
	}
	return srv
}

func TestRegisterRulesToolsAttachesAll(t *testing.T) {
	srv := newRulesServer(t)
	for _, want := range []string{
		"tokenops_rules_analyze",
		"tokenops_rules_conflicts",
		"tokenops_rules_compress",
		"tokenops_rules_inject",
	} {
		if _, ok := srv.GetTool(want); !ok {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestRulesAnalyzeMCP(t *testing.T) {
	dir := writeMCPRuleCorpus(t)
	srv := newRulesServer(t)
	out := execTool(t, srv, "tokenops_rules_analyze", map[string]string{"root": dir, "repo_id": "repo"})
	for _, want := range []string{"CLAUDE.md", "AGENTS.md", "duplicate_groups"} {
		if !strings.Contains(out, want) {
			t.Errorf("analyze output missing %q:\n%s", want, out)
		}
	}
}

func TestRulesConflictsMCP(t *testing.T) {
	dir := writeMCPRuleCorpus(t)
	srv := newRulesServer(t)
	out := execTool(t, srv, "tokenops_rules_conflicts", map[string]string{"root": dir, "repo_id": "repo"})
	if !strings.Contains(out, "findings") {
		t.Errorf("conflicts output missing findings:\n%s", out)
	}
}

func TestRulesCompressMCP(t *testing.T) {
	dir := writeMCPRuleCorpus(t)
	srv := newRulesServer(t)
	out := execTool(t, srv, "tokenops_rules_compress", map[string]any{"root": dir, "repo_id": "repo"})
	if !strings.Contains(out, "original_tokens") {
		t.Errorf("compress output missing original_tokens:\n%s", out)
	}
}

func TestRulesInjectMCP(t *testing.T) {
	dir := writeMCPRuleCorpus(t)
	srv := newRulesServer(t)
	out := execTool(t, srv, "tokenops_rules_inject", map[string]any{
		"root":     dir,
		"repo_id":  "repo",
		"keywords": []string{"testing"},
	})
	if !strings.Contains(out, "Selections") {
		t.Errorf("inject output missing Selections:\n%s", out)
	}
}
