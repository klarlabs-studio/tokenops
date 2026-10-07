package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeGoldenRuleTree writes a corpus with one duplicated section body
// (Testing), one drifting anchor (Style) and one competing-incentive pair
// (concise against thorough), so every rules question has an answer.
// Exactly one duplicate group keeps the text renderings, which range maps,
// deterministic.
func writeGoldenRuleTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"CLAUDE.md": "Repo rules for the agent.\n\n## Testing\nuse tdd everywhere and keep tests beside the code\n\n" +
			"## Style\nbe concise in every reply\n\n## Review\nexplain thoroughly with detailed reasoning\n",
		"AGENTS.md":            "Agent notes.\n\n## Testing\nuse tdd everywhere and keep tests beside the code\n\n## Style\nfollow go conventions\n",
		".cursor/rules/go.mdc": "Go rules.\n\n## Go\nrun gofmt and go vet before every commit\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// elapsed masks the router's wall-clock timing.
var elapsed = regexp.MustCompile(`elapsed_ns=\d+|"ElapsedNS":\d+`)

// TestRulesOutputCharacterization pins every `tokenops rules` subcommand
// byte for byte, so moving them onto capability/ruleintel cannot change
// what an operator reads.
func TestRulesOutputCharacterization(t *testing.T) {
	root := writeGoldenRuleTree(t)
	specDir := t.TempDir()
	spec := "profiles:\n  - name: repo\n    root: " + root + "\n    repo_id: repo\n    min_score: 0.0\n" +
		"scenarios:\n  - name: tdd\n    repo_id: repo\n    keywords: [testing]\n" +
		"    exposure:\n      requests: 100\n      output_tokens: 5000\n      baseline_output_tokens: 6500\n      retries: 3\n"
	specPath := filepath.Join(specDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	in := []string{"--root", root, "--repo-id", "repo"}
	cases := []struct {
		golden string
		args   []string
	}{
		{"rules/analyze.txt", append([]string{"rules", "analyze"}, in...)},
		{"rules/analyze.json", append([]string{"rules", "analyze", "--json", "--provider", "anthropic"}, in...)},
		{"rules/conflicts.txt", append([]string{"rules", "conflicts"}, in...)},
		{"rules/conflicts.json", append([]string{"rules", "conflicts", "--json"}, in...)},
		{"rules/compress.txt", append([]string{"rules", "compress"}, in...)},
		{"rules/compress.json", append([]string{"rules", "compress", "--json"}, in...)},
		{"rules/compress_body.json", append([]string{"rules", "compress", "--json", "--emit-body", "--similarity", "0.5", "--quality-floor", "0.9"}, in...)},
		{"rules/inject.txt", append([]string{"rules", "inject", "--keyword", "testing", "--file", "main_test.go"}, in...)},
		{"rules/inject.json", append([]string{"rules", "inject", "--json", "--keyword", "style", "--token-budget", "40", "--min-score", "0", "--include-global=false", "--latency-budget-ms", "500"}, in...)},
		{"rules/bench.txt", []string{"rules", "bench", "--spec", specPath}},
		{"rules/bench.json", []string{"rules", "bench", "--spec", specPath, "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out, err := executeRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			out = strings.ReplaceAll(out, root, "<ROOT>")
			out = elapsed.ReplaceAllStringFunc(out, func(m string) string {
				return m[:strings.IndexAny(m, "=:")+1] + "0"
			})
			if strings.HasSuffix(tc.golden, ".json") {
				out = roundFloats(out)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}
