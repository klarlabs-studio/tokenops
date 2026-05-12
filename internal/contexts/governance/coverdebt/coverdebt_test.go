package coverdebt

import (
	"slices"
	"strings"
	"testing"
)

const sampleProfile = `mode: set
github.com/felixgeelhaar/tokenops/internal/daemon/daemon.go:1.1,1.2 5 0
github.com/felixgeelhaar/tokenops/internal/daemon/daemon.go:2.1,2.2 5 1
github.com/felixgeelhaar/tokenops/internal/contexts/rules/router.go:1.1,1.2 10 1
github.com/felixgeelhaar/tokenops/internal/contexts/rules/router.go:2.1,2.2 10 1
github.com/felixgeelhaar/tokenops/internal/contexts/rules/router.go:3.1,3.2 10 0
github.com/felixgeelhaar/tokenops/internal/config/config.go:1.1,1.2 4 1
`

func TestParseCoverProfile(t *testing.T) {
	rows, err := ParseCoverProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 packages, got %d (%+v)", len(rows), rows)
	}
	want := map[string]struct {
		stmts, covered int64
	}{
		"github.com/felixgeelhaar/tokenops/internal/daemon":         {10, 5},
		"github.com/felixgeelhaar/tokenops/internal/contexts/rules": {30, 20},
		"github.com/felixgeelhaar/tokenops/internal/config":         {4, 4},
	}
	for _, r := range rows {
		w, ok := want[r.Package]
		if !ok {
			t.Errorf("unexpected package %q", r.Package)
			continue
		}
		if r.Statements != w.stmts || r.CoveredStatements != w.covered {
			t.Errorf("%s = (%d,%d), want (%d,%d)",
				r.Package, r.Statements, r.CoveredStatements, w.stmts, w.covered)
		}
	}
}

func TestAnalyzeFlagsHighRiskGoalMisses(t *testing.T) {
	cov, err := ParseCoverProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	report := Analyze(cov, DefaultPolicies)
	if len(report.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(report.Rows))
	}
	// internal/daemon at 50% coverage, goal 50%, should be met.
	// internal/rules at 66.7% coverage, goal 65%, should be met.
	// internal/config at 100%, goal 85%, met.
	if len(report.Failed) != 0 {
		t.Errorf("expected no failures with stub coverage, got %v", report.Failed)
	}
	// Sorted by RiskScore desc — daemon (critical, half-uncovered) leads.
	if report.Rows[0].Package != "github.com/felixgeelhaar/tokenops/internal/daemon" {
		t.Errorf("top row = %s, want daemon", report.Rows[0].Package)
	}
}

func TestAnalyzeRecordsFailures(t *testing.T) {
	cov := []Coverage{
		{Package: "github.com/felixgeelhaar/tokenops/internal/daemon", Statements: 100, CoveredStatements: 10},
	}
	report := Analyze(cov, DefaultPolicies)
	if !slices.Contains(report.Failed, "github.com/felixgeelhaar/tokenops/internal/daemon") {
		t.Errorf("expected daemon in failed list, got %v", report.Failed)
	}
	if report.Rows[0].GoalMet {
		t.Errorf("expected GoalMet=false")
	}
	if report.Rows[0].Gap < 39 || report.Rows[0].Gap > 41 {
		t.Errorf("Gap = %f, want ~40", report.Rows[0].Gap)
	}
}

func TestAnalyzeOverallScoreIsRiskWeighted(t *testing.T) {
	cov := []Coverage{
		{Package: "github.com/felixgeelhaar/tokenops/internal/daemon", Statements: 100, CoveredStatements: 0},
		{Package: "github.com/felixgeelhaar/tokenops/cmd/tokenops", Statements: 100, CoveredStatements: 100},
	}
	report := Analyze(cov, DefaultPolicies)
	// daemon=Risk10 cov=0, cmd/tokenops=Risk1 cov=100 → overall = (0*10 + 100*1)/(10+1) ≈ 9.09
	if report.OverallScore < 8 || report.OverallScore > 10 {
		t.Errorf("OverallScore = %f, want ~9", report.OverallScore)
	}
}

func TestUnknownPackageDefaultsMedium(t *testing.T) {
	cov := []Coverage{
		{Package: "github.com/felixgeelhaar/tokenops/internal/unknownnew", Statements: 100, CoveredStatements: 50},
	}
	report := Analyze(cov, DefaultPolicies)
	if report.Rows[0].Risk != RiskMedium {
		t.Errorf("unknown package risk = %v, want medium", report.Rows[0].Risk)
	}
	if report.Rows[0].Goal != 60 {
		t.Errorf("unknown package goal = %f, want 60", report.Rows[0].Goal)
	}
}

func TestRiskString(t *testing.T) {
	if RiskCritical.String() != "critical" {
		t.Errorf("RiskCritical = %q", RiskCritical.String())
	}
	if RiskHigh.String() != "high" {
		t.Errorf("RiskHigh = %q", RiskHigh.String())
	}
}
