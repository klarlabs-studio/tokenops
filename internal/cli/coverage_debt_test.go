package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goldProfile = `mode: set
github.com/felixgeelhaar/tokenops/internal/daemon/daemon.go:1.1,1.2 100 0
github.com/felixgeelhaar/tokenops/internal/contexts/rules/router.go:1.1,1.2 100 1
`

func TestCoverageDebtCLIJSON(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(profile, []byte(goldProfile), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	out, err := executeRoot(t, "coverage-debt", "--profile", profile, "--json")
	if err != nil {
		t.Fatalf("coverage-debt: %v", err)
	}
	var payload struct {
		Rows []struct {
			Package  string  `json:"package"`
			GoalMet  bool    `json:"goal_met"`
			Coverage float64 `json:"coverage_pct"`
		} `json:"rows"`
		Failed []string `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(payload.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(payload.Rows))
	}
	hitDaemon := false
	for _, r := range payload.Rows {
		if strings.HasSuffix(r.Package, "/daemon") {
			hitDaemon = true
			if r.GoalMet {
				t.Errorf("daemon at 0%% should miss goal")
			}
		}
	}
	if !hitDaemon {
		t.Errorf("expected daemon row")
	}
	if len(payload.Failed) == 0 {
		t.Errorf("expected non-empty failed list, got %s", out)
	}
}

func TestCoverageDebtCLIEnforce(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(profile, []byte(goldProfile), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	_, err := executeRoot(t, "coverage-debt", "--profile", profile, "--enforce")
	if err == nil {
		t.Fatal("expected enforcement failure")
	}
	if !strings.Contains(err.Error(), "coverage gate failed") {
		t.Errorf("err = %q, want coverage gate failed", err.Error())
	}
}
