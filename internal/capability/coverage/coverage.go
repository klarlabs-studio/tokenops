// Package coverage ranks TokenOps' own test-coverage debt: each package's
// coverage against the goal its risk sets. `tokenops coverage-debt` asks
// it; it is a tool for working on TokenOps itself.
package coverage

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt"
)

// Report is the risk-ranked debt, with the packages below their goal.
type Report = coverdebt.Report

// Debt reads the Go cover profile at path and ranks each package's debt
// against the default per-risk goals.
func Debt(path string) (*Report, error) {
	cov, err := coverdebt.ReadProfile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	return coverdebt.Analyze(cov, coverdebt.DefaultPolicies), nil
}
