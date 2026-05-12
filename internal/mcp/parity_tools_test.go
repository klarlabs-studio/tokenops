package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

func newParityServer(t *testing.T, deps ParityDeps) *Server {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterParityTools(srv, deps); err != nil {
		t.Fatalf("RegisterParityTools: %v", err)
	}
	return srv
}

func TestParityToolsAttached(t *testing.T) {
	srv := newParityServer(t, ParityDeps{})
	wantBase := []string{
		"tokenops_rules_bench",
		"tokenops_eval",
		"tokenops_coverage_debt",
		"tokenops_scorecard",
	}
	for _, name := range wantBase {
		if _, ok := srv.GetTool(name); !ok {
			t.Errorf("missing tool %q", name)
		}
	}
	if _, ok := srv.GetTool("tokenops_replay"); ok {
		t.Errorf("replay should not be registered without deps")
	}
}

func TestParityReplayRegisteredWhenDepsPresent(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := newParityServer(t, ParityDeps{Store: store, Spend: spend.NewEngine(spend.DefaultTable())})
	if _, ok := srv.GetTool("tokenops_replay"); !ok {
		t.Errorf("expected tokenops_replay when deps present")
	}
}

func TestParityRulesBenchHandler(t *testing.T) {
	srv := newParityServer(t, ParityDeps{})
	root := writeBenchCorpus(t)
	// Marshal the path through json so Windows backslashes are escaped.
	rootJSON, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal root: %v", err)
	}
	spec := `{
"profiles":[{"name":"lean","root":` + string(rootJSON) + `,"repo_id":"repo","min_score":0.0}],
"scenarios":[{"name":"tdd","repo_id":"repo","keywords":["testing"],"exposure":{"requests":100,"output_tokens":5000,"baseline_output_tokens":6500,"retries":3}}]
}`
	out := execTool(t, srv, "tokenops_rules_bench", map[string]string{"spec_json": spec})
	if !strings.Contains(out, `"Winners"`) {
		t.Errorf("output missing Winners: %s", out)
	}
}

func writeBenchCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"),
		[]byte("# Testing\nuse tdd\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func TestParityEvalHandler(t *testing.T) {
	wd, _ := os.Getwd()
	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))
	t.Chdir(repoRoot)
	srv := newParityServer(t, ParityDeps{})
	out := execTool(t, srv, "tokenops_eval", map[string]any{})
	for _, want := range []string{`"report"`, `"gate"`, `"total_cases"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in eval output: %s", want, out)
		}
	}
}

func TestParityCoverageDebtHandler(t *testing.T) {
	srv := newParityServer(t, ParityDeps{})
	dir := t.TempDir()
	profile := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(profile, []byte(
		"mode: set\ngithub.com/felixgeelhaar/tokenops/internal/daemon/daemon.go:1.1,1.2 100 0\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := execTool(t, srv, "tokenops_coverage_debt", map[string]string{"profile": profile})
	if !strings.Contains(out, `"rows"`) || !strings.Contains(out, "daemon") {
		t.Errorf("missing rows/daemon in output: %s", out)
	}
}

func TestParityScorecardHandlerNilStore(t *testing.T) {
	srv := newParityServer(t, ParityDeps{})
	out := execTool(t, srv, "tokenops_scorecard", map[string]any{"fvt_seconds": 30, "teu_pct": 25, "sac_pct": 95})
	if !strings.Contains(out, `"overall_grade"`) {
		t.Errorf("missing overall_grade: %s", out)
	}
}
