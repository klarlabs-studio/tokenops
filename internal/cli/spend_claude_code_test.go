package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/claudecodeorg"
)

func TestWriteClaudeCodeReport(t *testing.T) {
	day := claudecodeorg.Day{Day: "2026-10-31", Users: 3, APIKeys: 1, Sessions: 12, Commits: 5, PullRequests: 2,
		LinesAdded: 900, LinesRemoved: 300, Tools: map[string]claudecodeorg.ToolDecisions{"edit_tool": {Accepted: 9, Rejected: 1}},
		Models: map[string]claudecodeorg.Model{"claude-opus-5-5": {InputTokens: 1000, EstimatedUSD: 4.5}}}
	rep := claudecodeorg.Report{Since: "2026-10-31", Until: "2026-10-31", Scope: claudecodeorg.Scope, Days: []claudecodeorg.Day{day}, Total: day}
	var buf bytes.Buffer
	writeClaudeCodeReport(&buf, rep)
	out := buf.String()
	for _, want := range []string{"2026-10-31", "90%", "$4.50", "subscription"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestSpendClaudeCodeNeedsAnAdminKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TOKENOPS_ANTHROPIC_ADMIN_KEY", "")
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := executeRoot(t, "--config", cfg, "spend", "claude-code")
	if err == nil || !strings.Contains(err.Error(), "admin key") {
		t.Errorf("err = %v", err)
	}
}
