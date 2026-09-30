package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
)

// parsePlanStart reads a plan's start date: a day (2026-09-01, midnight UTC)
// or an RFC3339 instant. Empty means now.
func parsePlanStart(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q: want a date (2026-09-01) or an RFC3339 time", s)
	}
	return t.UTC(), nil
}

// recordPlanChange writes a plan switch to the history and, for a
// backdated one, re-marks the usage recorded as billed since then. The
// config write has already happened; a failure here is reported but
// does not undo it, because the plan in force now is correct either way.
func recordPlanChange(ctx context.Context, out io.Writer, provider, previous, plan string, from, now time.Time, dbFlag string) error {
	dbPath, _ := resolvePlanDB(dbFlag)
	res, err := planhistory.Record(ctx, planhistory.Switch{
		Provider: provider, Previous: previous, Plan: plan, From: from, Now: now, DBPath: dbPath, Actor: "cli",
	})
	if res.StoreErr != nil {
		fmt.Fprintf(out, "warning: event store unavailable, so earlier usage was not re-marked: %v\n", res.StoreErr)
	}
	if err != nil {
		return err
	}
	if from.Before(now) && plan != "" && res.StoreErr == nil {
		fmt.Fprintf(out, "%s since %s; re-marked %d earlier call(s) from billed to plan-covered\n",
			plan, from.Format("2006-01-02"), res.Restamped)
	}
	return nil
}

func newPlanHistoryCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List recorded plan switches, oldest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := planhistory.Default()
			if err != nil {
				return err
			}
			h, err := file.Load()
			if err != nil {
				return err
			}
			if jsonOut {
				if h == nil {
					h = plans.History{}
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(h)
			}
			if len(h) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no plan switches recorded; the configured plans apply to all history")
				return nil
			}
			for _, b := range h {
				from := "the start"
				if !b.From.IsZero() {
					from = b.From.Format("2006-01-02")
				}
				plan := b.Plan
				if plan == "" {
					plan = "(no plan)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %-18s from %s (recorded %s)\n",
					b.Provider, plan, from, b.Recorded.Format("2006-01-02"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
