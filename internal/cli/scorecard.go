package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/scorecard"
)

func newScorecardCmd() *cobra.Command {
	var jsonOut bool
	var baselineRef string

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
			// TODO(feature): wire into analytics / spend / replay engines
			// to compute live values from the event store. The current
			// implementation returns the scorecard constructor with
			// placeholder values — replace with real data queries once
			// the data-pipeline plumbing is in place.
			_ = baselineRef

			s := scorecard.New(45, 15, 80, baselineRef)

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
	return cmd
}
