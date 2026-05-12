// Package coverdebt computes a risk-ranked test coverage debt report from
// a Go coverage profile (`go test -coverprofile`). It complements the
// quality-evals framework: where eval gates regression in optimizer
// output, coverdebt gates regression in test reach over the packages
// that matter most.
//
// The scoring model lives in docs/coverage-debt.md:
//
//	Risk Score = Impact × (1 - Coverage)
//
// Impact buckets (10/7/4/1) reflect blast radius — Critical packages stop
// the daemon, Low packages are thin wrappers. The CLI subcommand
// `tokenops coverage-debt` prints a debt-ranked table and (with
// --enforce) exits non-zero when any package misses its coverage goal.
package coverdebt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Risk classifies a package's blast radius if its code regresses.
type Risk int

// Known risk levels mirror docs/coverage-debt.md.
const (
	RiskLow      Risk = 1
	RiskMedium   Risk = 4
	RiskHigh     Risk = 7
	RiskCritical Risk = 10
)

// String returns the risk label.
func (r Risk) String() string {
	switch r {
	case RiskCritical:
		return "critical"
	case RiskHigh:
		return "high"
	case RiskMedium:
		return "medium"
	default:
		return "low"
	}
}

// PackagePolicy is the policy for a single package: its risk level and the
// coverage goal in percent (0–100) it must meet.
type PackagePolicy struct {
	Package string
	Risk    Risk
	// Goal is the coverage percentage the package must meet to clear the
	// gate. Zero means the package is excluded from gate enforcement but
	// still appears in the debt report.
	Goal float64
}

// DefaultPolicies seeds risk + goal classifications for known TokenOps
// packages. Unknown packages default to RiskMedium / Goal 60 — surfaces
// new code instead of silently hiding it.
//
// Updating this table is the canonical place to encode the rubric;
// docs/coverage-debt.md reflects it.
var DefaultPolicies = []PackagePolicy{
	// Critical
	{Package: "github.com/felixgeelhaar/tokenops/internal/daemon", Risk: RiskCritical, Goal: 50},
	{Package: "github.com/felixgeelhaar/tokenops/cmd/tokenopsd", Risk: RiskCritical, Goal: 50},
	{Package: "github.com/felixgeelhaar/tokenops/internal/storage/sqlite", Risk: RiskCritical, Goal: 70},
	// High
	{Package: "github.com/felixgeelhaar/tokenops/internal/proxy", Risk: RiskHigh, Goal: 65},
	{Package: "github.com/felixgeelhaar/tokenops/internal/mcp", Risk: RiskHigh, Goal: 60},
	{Package: "github.com/felixgeelhaar/tokenops/internal/otlp", Risk: RiskHigh, Goal: 70},
	{Package: "github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer", Risk: RiskHigh, Goal: 65},
	{Package: "github.com/felixgeelhaar/tokenops/internal/contexts/rules", Risk: RiskHigh, Goal: 65},
	{Package: "github.com/felixgeelhaar/tokenops/internal/contexts/security/redaction", Risk: RiskHigh, Goal: 70},
	{Package: "github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics", Risk: RiskHigh, Goal: 65},
	{Package: "github.com/felixgeelhaar/tokenops/internal/events", Risk: RiskHigh, Goal: 70},
	// Medium
	{Package: "github.com/felixgeelhaar/tokenops/internal/cli", Risk: RiskMedium, Goal: 60},
	{Package: "github.com/felixgeelhaar/tokenops/internal/config", Risk: RiskMedium, Goal: 85},
	{Package: "github.com/felixgeelhaar/tokenops/internal/contexts/governance/scorecard", Risk: RiskMedium, Goal: 70},
	{Package: "github.com/felixgeelhaar/tokenops/internal/contexts/optimization/eval", Risk: RiskMedium, Goal: 60},
	{Package: "github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer", Risk: RiskMedium, Goal: 70},
	// Low
	{Package: "github.com/felixgeelhaar/tokenops/cmd/tokenops", Risk: RiskLow, Goal: 30},
}

// Coverage holds the parsed per-package coverage in percent (0–100).
type Coverage struct {
	Package           string
	Statements        int64
	CoveredStatements int64
}

// Pct returns the coverage as a percentage [0, 100].
func (c Coverage) Pct() float64 {
	if c.Statements == 0 {
		return 0
	}
	return float64(c.CoveredStatements) / float64(c.Statements) * 100
}

// DebtRow is one row of the debt report.
type DebtRow struct {
	Package   string  `json:"package"`
	Risk      Risk    `json:"risk"`
	Coverage  float64 `json:"coverage_pct"`
	Goal      float64 `json:"goal_pct"`
	RiskScore float64 `json:"risk_score"`
	Gap       float64 `json:"gap_pct"`
	GoalMet   bool    `json:"goal_met"`
}

// Report bundles a coverage-debt analysis.
type Report struct {
	Rows         []DebtRow `json:"rows"`
	OverallScore float64   `json:"overall_score"`
	TotalRisk    float64   `json:"total_risk"`
	Failed       []string  `json:"failed,omitempty"`
}

// ParseCoverProfile reads a Go cover profile from r and returns per-package
// Coverage rows. Format reference:
// https://pkg.go.dev/cmd/go/internal/cover (cover.profile).
func ParseCoverProfile(r io.Reader) ([]Coverage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	first := true
	byPkg := map[string]*Coverage{}
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			if !strings.HasPrefix(line, "mode:") {
				return nil, fmt.Errorf("coverdebt: expected 'mode:' header, got %q", line)
			}
			continue
		}
		if line == "" {
			continue
		}
		// Format: file:startLine.startCol,endLine.endCol numStatements count
		// We want the file segment up to the last ":".
		pathEnd := strings.LastIndex(line, ":")
		if pathEnd <= 0 {
			continue
		}
		filePath := line[:pathEnd]
		fields := strings.Fields(line[pathEnd+1:])
		if len(fields) < 3 {
			continue
		}
		stmts := atoi64(fields[len(fields)-2])
		count := atoi64(fields[len(fields)-1])
		pkg := packageFromFile(filePath)
		c := byPkg[pkg]
		if c == nil {
			c = &Coverage{Package: pkg}
			byPkg[pkg] = c
		}
		c.Statements += stmts
		if count > 0 {
			c.CoveredStatements += stmts
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make([]Coverage, 0, len(byPkg))
	for _, c := range byPkg {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out, nil
}

// ReadProfile is a convenience wrapper for the common case of reading a
// profile from a file path.
func ReadProfile(path string) ([]Coverage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseCoverProfile(f)
}

// Analyze applies policies to coverage data and returns the debt report.
// Packages observed in coverage but absent from policies fall through to
// RiskMedium / Goal 60.
func Analyze(coverage []Coverage, policies []PackagePolicy) *Report {
	policyMap := make(map[string]PackagePolicy, len(policies))
	for _, p := range policies {
		policyMap[p.Package] = p
	}
	r := &Report{}
	var weightedSum float64
	for _, c := range coverage {
		policy, ok := policyMap[c.Package]
		if !ok {
			policy = PackagePolicy{Risk: RiskMedium, Goal: 60}
		}
		covPct := c.Pct()
		row := DebtRow{
			Package:   c.Package,
			Risk:      policy.Risk,
			Coverage:  covPct,
			Goal:      policy.Goal,
			RiskScore: float64(policy.Risk) * (1 - covPct/100),
			Gap:       policy.Goal - covPct,
			GoalMet:   policy.Goal == 0 || covPct >= policy.Goal,
		}
		if row.Gap < 0 {
			row.Gap = 0
		}
		r.Rows = append(r.Rows, row)
		if !row.GoalMet {
			r.Failed = append(r.Failed, c.Package)
		}
		weightedSum += covPct * float64(policy.Risk)
		r.TotalRisk += float64(policy.Risk)
	}
	if r.TotalRisk > 0 {
		r.OverallScore = weightedSum / r.TotalRisk
	}
	sort.SliceStable(r.Rows, func(i, j int) bool {
		return r.Rows[i].RiskScore > r.Rows[j].RiskScore
	})
	return r
}

func packageFromFile(file string) string {
	// Trim trailing filename so we get the import path.
	idx := strings.LastIndex(file, "/")
	if idx <= 0 {
		return file
	}
	return file[:idx]
}

func atoi64(s string) int64 {
	var n int64
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < '0' || ch > '9' {
			return n
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}
