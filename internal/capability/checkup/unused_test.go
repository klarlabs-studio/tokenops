package checkup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transcript writes one Claude Code session that loads a plugin with two
// skills, an MCP server with instructions, a plugin's sign-in server and
// a connector, and makes turns assistant turns that call the tools in use.
func transcript(t *testing.T, dir, session string, turns int, use []string) {
	t.Helper()
	line := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ts := t0.Format(time.RFC3339)
	lines := []string{
		line(map[string]any{"type": "attachment", "timestamp": ts, "attachment": map[string]any{
			"type": "skill_listing", "isInitial": true,
			"content": "- sales:call-prep: " + strings.Repeat("p", 4000) + "\n- sales:forecast\n- simplify: built in, left out\n",
		}}),
		line(map[string]any{"type": "attachment", "timestamp": ts, "attachment": map[string]any{
			"type": "deferred_tools_delta",
			"addedNames": []string{"mcp__kubectl__get", "mcp__kubectl__logs", "mcp__plugin_sales_crm__authenticate",
				"mcp__claude_ai_Gmail__send", "mcp__gone__x", "Monitor"},
			"removedNames": []string{"mcp__gone__x"},
		}}),
		line(map[string]any{"type": "attachment", "timestamp": ts, "attachment": map[string]any{
			"type": "mcp_instructions_delta", "addedNames": []string{"kubectl"},
			"addedBlocks": []string{strings.Repeat("k", 8000)},
		}}),
	}
	for i := 0; i < turns; i++ {
		var content []map[string]any
		if i < len(use) {
			if skill, ok := strings.CutPrefix(use[i], "skill:"); ok {
				content = append(content, map[string]any{"type": "tool_use", "name": "Skill", "input": map[string]any{"skill": skill}})
			} else {
				content = append(content, map[string]any{"type": "tool_use", "name": use[i], "input": map[string]any{}})
			}
		}
		lines = append(lines, line(map[string]any{"type": "assistant", "timestamp": t0.Add(time.Minute).Format(time.RFC3339),
			"message": map[string]any{"id": fmt.Sprintf("%s-%d", session, i), "usage": map[string]any{"input_tokens": 1}, "content": content}}))
	}
	path := filepath.Join(dir, session+".jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// What was loaded in at least five sessions and never used is named,
// largest first, with the command that unloads it; what was used once,
// what a delta removed, built-in skills and subagents are not.
func TestUnusedContext(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "-p")
	for i := 0; i < 5; i++ {
		var use []string
		if i == 0 {
			use = []string{"mcp__kubectl__get"}
		}
		transcript(t, project, fmt.Sprintf("s%d", i), 4, use)
	}
	sub := filepath.Join(project, "s0", "subagents")
	transcript(t, sub, "agent-1", 2, []string{"skill:sales:forecast"})

	owners := readLoadouts(context.Background(), root, nil, t0.Add(-time.Hour))
	claudeDir := t.TempDir()
	synced := filepath.Join(claudeDir, "plugins", "synced", "acct")
	if err := os.MkdirAll(synced, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(synced, "manifest.json"), []byte(`{"plugins":[{"name":"sales"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := unusedFinding(owners, claudeDir)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v", fs)
	}
	f := fs[0]
	if f.Kind != "unused_context" || f.Level != LevelInfo {
		t.Errorf("finding = %+v", f)
	}
	for _, want := range []string{"2 loaded into every turn", "plugin sales (1k tokens, 5 sessions)", "connector Gmail"} {
		if !strings.Contains(f.Evidence, want) {
			t.Errorf("evidence %q lacks %q", f.Evidence, want)
		}
	}
	for _, unwanted := range []string{"kubectl", "gone", "simplify", "Monitor"} {
		if strings.Contains(f.Evidence, unwanted) {
			t.Errorf("evidence %q names %q", f.Evidence, unwanted)
		}
	}
	if !strings.Contains(f.Fix, "`claude plugin disable sales@synced`") || !strings.Contains(f.Fix, "Gmail connector off") {
		t.Errorf("fix = %q", f.Fix)
	}
	// Turned off since, the plugin is no longer named.
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"enabledPlugins":{"sales@synced":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if off := unusedFinding(owners, claudeDir); len(off) != 1 || strings.Contains(off[0].Evidence, "sales") {
		t.Errorf("disabled plugin still named: %+v", off)
	}
	if !strings.HasPrefix(f.Evidence, "2 loaded") || strings.Index(f.Evidence, "sales") > strings.Index(f.Evidence, "Gmail") {
		t.Errorf("not largest first: %q", f.Evidence)
	}
}

// Four sessions are too few to call anything unused.
func TestUnusedContextNeedsFiveSessions(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		transcript(t, filepath.Join(root, "-p"), fmt.Sprintf("s%d", i), 3, nil)
	}
	if fs := unusedFinding(readLoadouts(context.Background(), root, nil, t0.Add(-time.Hour)), t.TempDir()); fs != nil {
		t.Errorf("judged on 4 sessions: %+v", fs)
	}
}

func TestOwnerOf(t *testing.T) {
	for server, want := range map[string]string{
		"plugin_product-management_amplitude": "plugin/product-management",
		"plugin_data_hex":                     "plugin/data",
		"claude_ai_Google_Drive":              "connector/Google Drive",
		"kubectl-mcp":                         "MCP server/kubectl-mcp",
	} {
		if kind, name := ownerOf(server); kind+"/"+name != want {
			t.Errorf("ownerOf(%q) = %s/%s, want %s", server, kind, name, want)
		}
	}
	if got := serverKey("claude.ai Claude Docs"); got != "claude_ai_Claude_Docs" {
		t.Errorf("serverKey = %q", got)
	}
}
