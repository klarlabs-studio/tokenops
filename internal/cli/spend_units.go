package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// newSpendUnitsCmd builds `tokenops spend units`: what a unit of finished
// work cost, against the operator's own four weeks before.
func newSpendUnitsCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath, sinceFlag string
		jsonOut           bool
	)
	cmd := &cobra.Command{
		Use:   "units",
		Short: "Cost per commit, merged pull request, outcome and change class, against your 4-week baseline",
		Long: `units prices finished work rather than tokens:

  - cost per commit, and per commit of each change class (feature, bugfix,
    refactor, test, docs, chore), read from Conventional Commits subjects
  - cost per merged pull request: squash "(#N)" and "Merge pull request #N"
    commits on the default branch, as git has fetched them
  - cost per recorded outcome, and per achieved one
  - on a flat-rate plan, each pull request and outcome as a share of the
    plan's longest window (an estimate from your own windows)

Each is set against the four weeks before the period. The joins are
heuristics, so the work no unit took is listed by why.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := resolveStorageReadPath(dbPath, cfg.Storage.Path)
			if err != nil {
				return err
			}
			now := time.Now()
			since := now.AddDate(0, 0, -uniteconomics.DefaultDays)
			if sinceFlag != "" {
				if since, err = parseSince(sinceFlag); err != nil {
					return fmt.Errorf("--since: %w", err)
				}
			}
			ctx := cmd.Context()
			store, err := sqlite.Open(ctx, path, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open event store: %w", err)
			}
			defer func() { _ = store.Close() }()
			eng, err := buildSpendEngine(cfg)
			if err != nil {
				return err
			}
			agg := spending.NewAggregator(store, eng)
			r, err := uniteconomics.Compute(ctx, uniteconomics.Deps{
				Turns: commits.TurnsIn(agg, spending.Window{}), Events: store,
			}, since, now)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, r)
			}
			writeUnitReport(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "start of the period (RFC3339 or duration such as 7d); default the last 7 days")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

var unitLabels = map[string]string{
	"commit": "commit", "merged_pr": "merged PR", "outcome": "recorded outcome", "achieved_outcome": "achieved outcome",
}

func writeUnitReport(out io.Writer, r uniteconomics.Report) {
	fmt.Fprintf(out, "Unit costs since %s, against the 4 weeks before\n\n", r.Since.Local().Format("2 Jan 15:04"))
	fmt.Fprintf(out, "  %-18s %9s %6s   %-16s %7s\n", "", "PER UNIT", "UNITS", "4-WEEK BASELINE", "CHANGE")
	units := []uniteconomics.Unit{r.Commits, r.PRs, r.Outcomes, r.Achieved}
	for _, u := range units {
		writeRateRow(out, unitLabels[u.Name], u.Current, u.Baseline, u.ChangePct)
	}
	if len(r.Classes) > 0 {
		fmt.Fprintln(out, "\n  By change class, per commit")
		for _, c := range r.Classes {
			writeRateRow(out, c.Class, c.Current, c.Baseline, c.ChangePct)
		}
	}
	var shares []string
	for _, u := range units {
		for _, s := range u.PlanShare {
			line := fmt.Sprintf("  %-18s %.1f%% of the %s %s window", unitLabels[u.Name], s.Pct, s.Provider, s.Window)
			if s.BaselinePct != nil {
				line += fmt.Sprintf("  (baseline %.1f%%)", *s.BaselinePct)
			}
			shares = append(shares, line)
		}
	}
	if len(shares) > 0 {
		fmt.Fprintln(out, "\n  Share of a plan window, per unit (estimated)")
		for _, s := range shares {
			fmt.Fprintln(out, s)
		}
	}
	fmt.Fprintln(out, "\n  Not placed on a unit this period")
	for _, u := range units[:3] {
		fmt.Fprintf(out, "  %-18s %.0f%% placed", unitLabels[u.Name], u.PlacedShare*100)
		for _, b := range u.Unplaced {
			if b.Units > 0 {
				fmt.Fprintf(out, " · %s: %d", b.Label, b.Units)
			} else {
				fmt.Fprintf(out, " · %s %s", b.Label, usd(b.APIEquivalentUSD))
			}
		}
		fmt.Fprintln(out)
	}
	writeFailures(out, r.Failures)
	fmt.Fprintln(out, "\nAt API prices. How work was joined:")
	for _, m := range []string{
		"commit: " + r.Commits.Method, "merged PR: " + r.PRs.Method,
		"outcome: " + r.Outcomes.Method, "class: " + r.ClassMethod, shareLine(r.ShareMethod),
		"did not stand: " + r.Failures.Method,
	} {
		if m != "" {
			fmt.Fprintf(out, "  %s\n", m)
		}
	}
}

// writeFailures says how much of the period went on work that did not
// stand, when any did.
func writeFailures(out io.Writer, f uniteconomics.Failures) {
	if f.Reverted.Turns == 0 && f.NotAchieved.Turns == 0 {
		return
	}
	fmt.Fprintf(out, "\n  Work that did not stand: %.0f%% of the period\n", f.Share*100)
	if f.Reverted.Turns > 0 {
		fmt.Fprintf(out, "  reverted within 14 days  %s on %d %s\n", usd(f.Reverted.APIEquivalentUSD), f.RevertedCommits, "commit"+plural(f.RevertedCommits))
	}
	if f.NotAchieved.Turns > 0 {
		fmt.Fprintf(out, "  outcome not achieved     %s on %d %s\n", usd(f.NotAchieved.APIEquivalentUSD), f.NotAchievedOutcomes, "execution"+plural(f.NotAchievedOutcomes))
	}
}

func shareLine(method string) string {
	if method == "" {
		return ""
	}
	return "plan share: " + method
}

func writeRateRow(out io.Writer, label string, cur, base uniteconomics.Rate, change *float64) {
	per, baseline, delta := "—", "—", ""
	if cur.PerUnitUSD != nil {
		per = usd(*cur.PerUnitUSD)
		if cur.UnpricedTurns > 0 {
			per += "+"
		}
	}
	if base.PerUnitUSD != nil {
		baseline = fmt.Sprintf("%s (%d)", usd(*base.PerUnitUSD), base.Units)
	}
	if change != nil {
		delta = fmt.Sprintf("%+.0f%%", *change)
	}
	fmt.Fprintf(out, "  %-18s %9s %6d   %-16s %7s\n", label, per, cur.Units, baseline, strings.TrimSpace(delta))
}
