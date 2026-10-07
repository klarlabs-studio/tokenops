package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func newScorecardCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	var baselineRef string
	var dbPath string
	var sinceDays int
	var fvtOverride float64
	var teuOverride float64
	var sacOverride float64

	cmd := &cobra.Command{
		Use:   "scorecard",
		Short: "Time to first value, token savings and spend attribution, graded A–F",
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
			store, closeStore, err := openScorecardStore(cmd.Context(), rf, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			s := spending.Scorecard(cmd.Context(), store, spending.ScorecardParams{
				SinceDays:   sinceDays,
				FVTSeconds:  fvtOverride,
				TEUPct:      teuOverride,
				SACPct:      sacOverride,
				BaselineRef: baselineRef,
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
	cmd.Flags().IntVar(&sinceDays, "days", 7, "window in days")
	cmd.Flags().Float64Var(&fvtOverride, "fvt-seconds", 0, "override First-Value Time in seconds")
	cmd.Flags().Float64Var(&teuOverride, "teu-pct", 0, "override Token Efficiency Uplift in percent")
	cmd.Flags().Float64Var(&sacOverride, "sac-pct", 0, "override Spend Attribution Completeness in percent")
	return cmd
}

// openScorecardStore opens the event store read for the live KPIs. A
// fresh install has none yet, which is not an error: the scorecard
// reports warming up. Opening would create an empty database, so a
// missing file returns a nil store instead.
func openScorecardStore(ctx context.Context, rf *rootFlags, flagPath string) (*sqlite.Store, func(), error) {
	path, err := resolveAuditDB(rf, flagPath)
	if err != nil {
		return nil, func() {}, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, func() {}, nil
	}
	openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	store, err := sqlite.Open(openCtx, path, sqlite.Options{})
	if err != nil {
		return nil, func() {}, fmt.Errorf("open events db: %w", err)
	}
	return store, func() { _ = store.Close() }, nil
}
