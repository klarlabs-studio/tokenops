package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/governance/coverdebt"
)

func newCoverageDebtCmd() *cobra.Command {
	var (
		profile string
		jsonOut bool
		enforce bool
	)
	cmd := &cobra.Command{
		Use:   "coverage-debt",
		Short: "Risk-ranked coverage debt report",
		Long: `coverage-debt parses a Go cover profile (from
"go test -coverprofile") and prints a risk-weighted debt report.

Each package's Risk Score is impact × (1 - coverage); impact buckets
are defined in docs/coverage-debt.md and seeded in
internal/coverdebt/coverdebt.go. With --enforce, any package missing
its per-risk goal causes a non-zero exit.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if profile == "" {
				return fmt.Errorf("--profile is required (run 'go test -coverprofile coverage.out ./...' first)")
			}
			cov, err := coverdebt.ReadProfile(profile)
			if err != nil {
				return fmt.Errorf("read profile: %w", err)
			}
			report := coverdebt.Analyze(cov, coverdebt.DefaultPolicies)
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
			}
			renderCoverageDebtText(cmd, report)
			if enforce && len(report.Failed) > 0 {
				return fmt.Errorf("coverage gate failed: %d package(s) below goal", len(report.Failed))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "coverage.out", "path to Go cover profile")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().BoolVar(&enforce, "enforce", false, "exit non-zero when any package misses its coverage goal")
	return cmd
}

func renderCoverageDebtText(cmd *cobra.Command, r *coverdebt.Report) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Overall Score: %.1f  (weighted by risk)\n", r.OverallScore)
	fmt.Fprintf(out, "Total Risk:    %.0f\n\n", r.TotalRisk)
	fmt.Fprintf(out, "%-56s %8s %8s %6s %6s %8s\n",
		"PACKAGE", "RISK", "COV%", "GOAL%", "GAP%", "SCORE")
	for _, row := range r.Rows {
		marker := " "
		if !row.GoalMet {
			marker = "!"
		}
		fmt.Fprintf(out, "%s%-55s %8s %8.1f %6.0f %6.1f %8.2f\n",
			marker, truncateRule(row.Package, 55),
			row.Risk.String(), row.Coverage, row.Goal, row.Gap, row.RiskScore)
	}
	if len(r.Failed) > 0 {
		fmt.Fprintf(out, "\n%d package(s) below goal:\n", len(r.Failed))
		for _, p := range r.Failed {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
}
