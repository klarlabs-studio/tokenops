package mcp

import (
	"log/slog"
	"os"
	"strings"
	"testing"
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
	if _, ok := srv.GetTool("tokenops_scorecard"); !ok {
		t.Error("missing tool tokenops_scorecard")
	}
	// The developer tools left MCP; they are CLI commands.
	for _, gone := range []string{"tokenops_rules_bench", "tokenops_eval", "tokenops_coverage_debt", "tokenops_replay"} {
		if _, ok := srv.GetTool(gone); ok {
			t.Errorf("%s is registered; it is a CLI-only developer tool", gone)
		}
	}
}

func TestParityScorecardHandlerNilStore(t *testing.T) {
	srv := newParityServer(t, ParityDeps{})
	out := execTool(t, srv, "tokenops_scorecard", map[string]any{"fvt_seconds": 30, "teu_pct": 25, "sac_pct": 95})
	if !strings.Contains(out, `"overall_grade"`) {
		t.Errorf("missing overall_grade: %s", out)
	}
}
