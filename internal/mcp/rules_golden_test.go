package mcp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// elapsedNS masks the router's wall-clock timing in an inject result.
var elapsedNS = regexp.MustCompile(`"(ElapsedNS|elapsed_ns)": \d+|elapsed_ns=\d+`)

// TestRulesToolsCharacterization pins the four rules tools, so moving them
// onto capability/ruleintel cannot change what an agent reads.
func TestRulesToolsCharacterization(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"CLAUDE.md": "Repo rules for the agent.\n\n## Testing\nuse tdd everywhere and keep tests beside the code\n\n" +
			"## Style\nbe concise in every reply\n\n## Review\nexplain thoroughly with detailed reasoning\n",
		"AGENTS.md":            "Agent notes.\n\n## Testing\nuse tdd everywhere and keep tests beside the code\n\n## Style\nfollow go conventions\n",
		".cursor/rules/go.mdc": "Go rules.\n\n## Go\nrun gofmt and go vet before every commit\n",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := newRulesServer(t)
	in := func(extra map[string]any) map[string]any {
		m := map[string]any{"root": root, "repo_id": "repo"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		golden string
		tool   string
		args   map[string]any
	}{
		{"rules/analyze.json", "tokenops_rules_analyze", in(nil)},
		{"rules/analyze_gemini.json", "tokenops_rules_analyze", in(map[string]any{"provider": "gemini"})},
		{"rules/conflicts.json", "tokenops_rules_conflicts", in(nil)},
		{"rules/compress.json", "tokenops_rules_compress", in(map[string]any{"similarity_threshold": 0.5, "quality_floor": 0.9})},
		{"rules/inject.json", "tokenops_rules_inject", in(map[string]any{"keywords": []string{"testing"}, "files": []string{"main_test.go"}})},
		{"rules/inject_budget.json", "tokenops_rules_inject", in(map[string]any{"keywords": []string{"style"}, "token_budget": 5, "include_global": true})},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out := execTool(t, srv, tc.tool, tc.args)
			out = strings.ReplaceAll(out, root, "<ROOT>")
			out = elapsedNS.ReplaceAllString(out, "<ELAPSED>")
			assertGolden(t, tc.golden, roundFloats(out))
		})
	}

	// Refusals are the caller's to fix and must read the same afterwards.
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"tokenops_rules_analyze", in(map[string]any{"provider": "cohere"})},
		{"tokenops_rules_conflicts", map[string]any{"root": filepath.Join(root, "absent")}},
	} {
		err := execToolErr(t, srv, tc.tool, tc.args)
		if err == nil {
			t.Fatalf("%s %v: no error", tc.tool, tc.args)
		}
		got := strings.ReplaceAll(err.Error(), root, "<ROOT>")
		want := map[string]string{
			"tokenops_rules_analyze":   `provider: value must be one of: [openai anthropic gemini]`,
			"tokenops_rules_conflicts": `root "<ROOT>/absent": stat <ROOT>/absent: no such file or directory`,
		}[tc.tool]
		if !strings.Contains(got, want) {
			t.Errorf("%s error = %q, want it to contain %q", tc.tool, got, want)
		}
	}
}
