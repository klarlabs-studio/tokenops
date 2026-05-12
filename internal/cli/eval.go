package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/eval"
)

func newEvalCmd() *cobra.Command {
	var (
		suitesGlob   string
		baselinePath string
		outputPath   string
		jsonOut      bool
		enforce      bool
		maxDrop      float64
		maxDrift     float64
		minCases     int
		filters      []string
	)
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the optimizer eval harness and gate against a baseline",
		Long: `eval loads suite fixtures (JSON) from --suites, runs the optimizer
pipeline against each case, and prints a per-suite + per-optimizer
quality report. When --baseline is provided the regression gate
compares the current report against the baseline and (with --enforce)
exits non-zero on any violation.

The default suite glob covers the bundled fixtures under
internal/eval/testdata. Use --output to persist the new report as the
next baseline.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			typed := make([]eval.OptimizationType, 0, len(filters))
			for _, f := range filters {
				typed = append(typed, eval.OptimizationType(f))
			}
			result, err := eval.Run(cmd.Context(), eval.RunParams{
				Suites:           suitesGlob,
				BaselinePath:     baselinePath,
				OptimizerFilters: typed,
				Gate: eval.Gate{
					MaxSuccessRateDropPct: maxDrop,
					MaxQualityDriftPct:    maxDrift,
					MinTotalCases:         minCases,
				},
			})
			if err != nil {
				return err
			}
			if outputPath != "" {
				if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
					return fmt.Errorf("write report: %w", err)
				}
				if err := eval.PersistBaseline(outputPath, result.Report); err != nil {
					return fmt.Errorf("write report: %w", err)
				}
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Report *eval.Report     `json:"report"`
					Gate   *eval.GateResult `json:"gate"`
				}{Report: result.Report, Gate: result.Gate})
			}
			renderEvalText(cmd, result.Suites, result.Report, result.Gate)
			if enforce && !result.Gate.Passed {
				return fmt.Errorf("eval gate failed: %d violation(s)", len(result.Gate.Violations))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&suitesGlob, "suites", "",
		"glob matching one or more JSON eval suites (defaults to bundled fixtures)")
	cmd.Flags().StringVar(&baselinePath, "baseline", "", "path to a previous report JSON; required for full gate enforcement")
	cmd.Flags().StringVar(&outputPath, "output", "", "write the merged report to this path (suitable as a future baseline)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().BoolVar(&enforce, "enforce", false, "exit non-zero on gate violations")
	cmd.Flags().Float64Var(&maxDrop, "max-success-drop-pct", 5.0, "maximum absolute drop in success rate (percentage points) vs baseline")
	cmd.Flags().Float64Var(&maxDrift, "max-quality-drift-pct", 10.0, "maximum relative AvgQuality regression (percent) vs baseline per optimizer")
	cmd.Flags().IntVar(&minCases, "min-cases", 1, "minimum total cases the run must cover for the gate to pass")
	cmd.Flags().StringSliceVar(&filters, "optimizer", nil, "restrict pipeline to these optimizer kinds (repeat for multiple)")
	return cmd
}

func renderEvalText(cmd *cobra.Command, suites []*eval.Suite, r *eval.Report, g *eval.GateResult) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "suites: %s\n", eval.SuiteNames(suites))
	fmt.Fprintf(out, "cases:  %d (passed=%d, success=%.2f%%)\n",
		r.TotalCases, r.PassedCases, r.SuccessRate)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%-22s %6s %6s %8s %10s %8s\n",
		"OPTIMIZER", "CASES", "PASS", "AVG_QUAL", "SAVED_TOK", "APPLY%")
	for k, s := range r.Optimizers {
		fmt.Fprintf(out, "%-22s %6d %6d %8.3f %10d %8.2f\n",
			k, s.TotalCases, s.PassedCases, s.AvgQuality, s.TotalSaved, s.ApplyRate)
	}
	fmt.Fprintln(out)
	if g == nil {
		return
	}
	if g.Passed {
		fmt.Fprintln(out, "gate: PASS")
	} else {
		fmt.Fprintf(out, "gate: FAIL (%d violation(s))\n", len(g.Violations))
		for _, v := range g.Violations {
			fmt.Fprintf(out, "  - %s [%s]: %s\n", v.Kind, v.Optimizer, v.Detail)
		}
	}
	if len(g.Drift) > 0 {
		fmt.Fprintln(out, "drift:")
		for k, d := range g.Drift {
			sign := "+"
			if d < 0 {
				sign = ""
			}
			fmt.Fprintf(out, "  %-22s %s%.2f%%\n", k, sign, d)
		}
	}
}
