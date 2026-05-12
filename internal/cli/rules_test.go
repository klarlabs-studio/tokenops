package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRuleTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"CLAUDE.md":             "# Repo Rules\n\n## Testing\nuse tdd everywhere\n\n## Style\nfollow go conventions\n",
		"AGENTS.md":             "# Agents\nfollow tdd\n",
		"docs/conventions/x.md": "# Convention\nbody\n",
		".cursor/rules/go.mdc":  "# Go\nbody\n",
		"README.md":             "ignored content\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func TestRulesAnalyzeTextOutput(t *testing.T) {
	root := writeRuleTree(t)
	out, err := executeRoot(t, "rules", "analyze", "--root", root, "--repo-id", "test")
	if err != nil {
		t.Fatalf("rules analyze: %v", err)
	}
	for _, want := range []string{"CLAUDE.md", "AGENTS.md", "claude_md", "agents_md", "Top sections"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "README.md") {
		t.Errorf("README.md should not appear in rules analyze output:\n%s", out)
	}
}

func TestRulesAnalyzeJSONOutput(t *testing.T) {
	root := writeRuleTree(t)
	out, err := executeRoot(t, "rules", "analyze", "--root", root, "--repo-id", "test", "--json")
	if err != nil {
		t.Fatalf("rules analyze --json: %v", err)
	}
	var payload struct {
		Documents []struct {
			Path        string `json:"Path"`
			TotalTokens int64  `json:"TotalTokens"`
			Sections    int    `json:"Sections"`
		} `json:"documents"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\noutput:\n%s", err, out)
	}
	if len(payload.Documents) < 3 {
		t.Errorf("documents = %d, want >= 3", len(payload.Documents))
	}
	for _, d := range payload.Documents {
		if d.TotalTokens <= 0 {
			t.Errorf("document %q has TotalTokens %d, want > 0", d.Path, d.TotalTokens)
		}
	}
}

func TestRulesConflictsDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"CLAUDE.md": "# Testing\nuse tdd everywhere\n",
		"AGENTS.md": "# Testing\nwrite tests after implementation\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	out, err := executeRoot(t, "rules", "conflicts", "--root", dir, "--repo-id", "test")
	if err != nil {
		t.Fatalf("rules conflicts: %v", err)
	}
	if !strings.Contains(out, "drift") {
		t.Errorf("expected drift finding in output:\n%s", out)
	}
	if !strings.Contains(out, "Testing") {
		t.Errorf("expected anchor in output:\n%s", out)
	}
}

func TestRulesConflictsJSONShape(t *testing.T) {
	dir := t.TempDir()
	body := "# Tone\nbe concise; explain thoroughly with detailed reasoning\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := executeRoot(t, "rules", "conflicts", "--root", dir, "--json")
	if err != nil {
		t.Fatalf("rules conflicts --json: %v", err)
	}
	var payload struct {
		Findings []struct {
			Kind    string   `json:"Kind"`
			Members []string `json:"Members"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, out)
	}
	if len(payload.Findings) == 0 {
		t.Fatalf("expected findings, got %s", out)
	}
}

func TestRulesCompressReportsTokenSavings(t *testing.T) {
	dir := t.TempDir()
	body := strings.Join([]string{
		"# Testing",
		"always use tdd",
		"",
		"# Testing",
		"always use tdd",
		"",
		"# Style",
		"prefer composition over inheritance and keep functions small enough to fit on a screen.",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := executeRoot(t, "rules", "compress", "--root", dir, "--json")
	if err != nil {
		t.Fatalf("rules compress: %v", err)
	}
	var payload struct {
		Results []struct {
			OriginalTokens   int64 `json:"original_tokens"`
			CompressedTokens int64 `json:"compressed_tokens"`
			Sections         []struct {
				Dropped       bool   `json:"Dropped"`
				DroppedReason string `json:"DroppedReason"`
			}
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, out)
	}
	if len(payload.Results) == 0 {
		t.Fatal("no results")
	}
	r := payload.Results[0]
	if r.OriginalTokens <= r.CompressedTokens {
		t.Errorf("expected savings: original=%d compressed=%d", r.OriginalTokens, r.CompressedTokens)
	}
	dropped := 0
	for _, s := range r.Sections {
		if s.Dropped {
			dropped++
		}
	}
	if dropped == 0 {
		t.Errorf("expected at least one dropped section in %+v", r.Sections)
	}
}

func TestRulesInjectSelectsByKeyword(t *testing.T) {
	dir := t.TempDir()
	body := "# Testing\nuse Go table-driven tests\n\n# Security\nnever leak secrets\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := executeRoot(t, "rules", "inject", "--root", dir, "--repo-id", "repo",
		"--keyword", "testing", "--json")
	if err != nil {
		t.Fatalf("rules inject: %v", err)
	}
	var payload struct {
		Selections []struct {
			SectionID string  `json:"SectionID"`
			Score     float64 `json:"Score"`
		} `json:"Selections"`
		Considered int `json:"Considered"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, out)
	}
	if payload.Considered == 0 {
		t.Errorf("considered = 0")
	}
	hit := false
	for _, s := range payload.Selections {
		if strings.Contains(s.SectionID, "Testing") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected Testing in selections: %+v", payload.Selections)
	}
}

func TestRulesBenchScoreboardFromSpec(t *testing.T) {
	dir := t.TempDir()
	leanDir := filepath.Join(dir, "lean")
	bloatDir := filepath.Join(dir, "bloat")
	if err := os.MkdirAll(leanDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(bloatDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(leanDir, "CLAUDE.md"),
		[]byte("# Testing\nuse tdd\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	bigBlock := strings.Repeat("Always run the full test suite, then lint, then commit with a conventional message; never skip hooks; document every public function. ", 6)
	if err := os.WriteFile(filepath.Join(bloatDir, "CLAUDE.md"),
		[]byte("# Testing\nuse tdd in every change set without exception\n## Security\n"+bigBlock+"\n## Logs\n"+bigBlock+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	spec := `
profiles:
  - name: lean
    root: ` + leanDir + `
    repo_id: repo
    min_score: 0.0
  - name: bloat
    root: ` + bloatDir + `
    repo_id: repo
    min_score: 0.0
scenarios:
  - name: tdd
    repo_id: repo
    keywords: [testing]
    exposure:
      requests: 100
      output_tokens: 5000
      baseline_output_tokens: 6500
      retries: 3
`
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	out, err := executeRoot(t, "rules", "bench", "--spec", specPath, "--json")
	if err != nil {
		t.Fatalf("rules bench: %v", err)
	}
	var payload struct {
		Scores []struct {
			Profile  string  `json:"Profile"`
			Scenario string  `json:"Scenario"`
			ROIScore float64 `json:"ROIScore"`
		} `json:"Scores"`
		Winners map[string]string `json:"Winners"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, out)
	}
	if len(payload.Scores) != 2 {
		t.Fatalf("scores = %d, want 2", len(payload.Scores))
	}
	if payload.Winners["tdd"] != "lean" {
		t.Errorf("winner = %q, want lean", payload.Winners["tdd"])
	}
}

func TestRulesAnalyzeRejectsUnknownProvider(t *testing.T) {
	root := writeRuleTree(t)
	_, err := executeRoot(t, "rules", "analyze", "--root", root, "--provider", "bogus")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
