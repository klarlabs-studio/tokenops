package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/governance/scorecard"
)

func newScorecardCmd() *cobra.Command {
	var jsonOut bool
	var baselineRef string
	var dbPath string
	var sinceDays int
	var fvtOverride float64
	var teuOverride float64
	var sacOverride float64

	cmd := &cobra.Command{
		Use:   "scorecard",
		Short: "Show the operator wedge KPI scorecard",
		Long: `scorecard computes and displays the operator wedge KPI scorecard,
which measures three key outcomes:

  First-Value Time (FVT)      — seconds from daemon start to first
                                observable result (proxy, spend, event).
                                Lower is better. Threshold: ≤60s green.

  Token Efficiency Uplift     — percentage reduction in tokens when the
    (TEU)                       optimizer pipeline is active vs. passive.
                                Higher is better. Threshold: ≥20% green.

  Spend Attribution           — percentage of total spend associated
    Completeness (SAC)          with a known workflow, agent, or session.
                                Higher is better. Threshold: ≥90% green.

The scorecard aggregates these three into an overall grade (A–F).

Use --capture-baseline (not yet implemented) to persist the current
values and --compare to diff against a stored baseline.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s := scorecard.Build(cmd.Context(), scorecard.BuildParams{
				DBPath:             dbPath,
				SinceDays:          sinceDays,
				FVTSecondsOverride: fvtOverride,
				TEUPctOverride:     teuOverride,
				SACPctOverride:     sacOverride,
				BaselineRef:        baselineRef,
			})

			if jsonOut {
				data, err := s.MarshalJSON()
				if err != nil {
					return fmt.Errorf("marshal scorecard: %w", err)
				}
				cmd.Println(string(data))
				return nil
			}
			cmd.Print(s.String())
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&baselineRef, "baseline-ref", "", "reference identifier for the baseline (version, date, or label)")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to ~/.tokenops/events.db)")
	cmd.Flags().IntVar(&sinceDays, "since-days", 7, "scorecard time window in days")
	cmd.Flags().Float64Var(&fvtOverride, "fvt-seconds", 0, "override First-Value Time in seconds")
	cmd.Flags().Float64Var(&teuOverride, "teu-pct", 0, "override Token Efficiency Uplift in percent")
	cmd.Flags().Float64Var(&sacOverride, "sac-pct", 0, "override Spend Attribution Completeness in percent")
	return cmd
}
