package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvalRunsBundledSuites(t *testing.T) {
	t.Chdir(repoRoot(t))
	out, err := executeRoot(t, "eval", "--json")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	var payload struct {
		Report struct {
			TotalCases  int     `json:"total_cases"`
			PassedCases int     `json:"passed_cases"`
			SuccessRate float64 `json:"success_rate"`
		} `json:"report"`
		Gate struct {
			Passed bool `json:"passed"`
		} `json:"gate"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, out)
	}
	if payload.Report.TotalCases == 0 {
		t.Fatalf("expected non-zero cases: %+v", payload)
	}
	if !payload.Gate.Passed {
		t.Errorf("gate should pass on first run without baseline: %+v", payload)
	}
}

func TestEvalEnforceBaselineRegression(t *testing.T) {
	t.Chdir(repoRoot(t))
	// First run: write baseline.
	baselinePath := filepath.Join(t.TempDir(), "baseline.json")
	if _, err := executeRoot(t, "eval", "--json", "--output", baselinePath); err != nil {
		t.Fatalf("baseline run: %v", err)
	}
	if _, err := os.Stat(baselinePath); err != nil {
		t.Fatalf("baseline not written: %v", err)
	}
	// Second run against same baseline should pass.
	if _, err := executeRoot(t, "eval", "--baseline", baselinePath, "--enforce"); err != nil {
		t.Errorf("stable run should pass gate, got %v", err)
	}
	// Mutate baseline so per_optimizer.prompt_compress.avg_quality is far
	// higher than what the live pipeline produces — forces a quality-drift
	// violation under the default 10% drift floor.
	var b map[string]any
	raw, _ := os.ReadFile(baselinePath)
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("unmarshal baseline: %v", err)
	}
	if opts, ok := b["per_optimizer"].(map[string]any); ok {
		opts["prompt_compress"] = map[string]any{
			"total_cases":          1,
			"passed_cases":         1,
			"avg_quality":          0.999,
			"total_savings_tokens": 1,
			"apply_rate":           0.0,
		}
	} else {
		t.Fatalf("baseline missing per_optimizer: %+v", b)
	}
	mut, _ := json.Marshal(b)
	if err := os.WriteFile(baselinePath, mut, 0o644); err != nil {
		t.Fatalf("rewrite baseline: %v", err)
	}
	out, err := executeRoot(t, "eval", "--baseline", baselinePath, "--enforce")
	if err == nil {
		t.Fatalf("expected enforcement failure: %s", out)
	}
	if !strings.Contains(err.Error(), "gate failed") {
		t.Errorf("error = %q, want gate failed", err.Error())
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/cli -> repo root
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
