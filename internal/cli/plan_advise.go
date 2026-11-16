package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func newPlanAdviseCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath  string
		jsonOut bool
		days    int
	)
	cmd := &cobra.Command{
		Use:   "advise",
		Short: "Which plan tier would have fit the last 30 days (advice only)",
		Long: `advise replays the last --days of each bound plan's windows against
the plan's other tiers (Claude Pro, Max, Ultra), each tier with its
overflow billed at API list price, and no subscription at all (the API at
list price). For each it shows how often you would have run out and for how
long, the fullest window, and the monthly cost; then it recommends one.

Readings taken before a plan switch are scaled by the plan in force then
(tokenops plan history). Claude tiers are the vendor's multiples of Pro's
allowance, and Pro's own allowance is unverified, so the answer carries a
confidence label. It is advice: TokenOps never switches a plan.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if days < 1 || days > headroom.MaxAdviceDays {
				return fmt.Errorf("--days must be from 1 to %d", headroom.MaxAdviceDays)
			}
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			resolved, err := resolvePlanDBIn(dbPath, cfg.Storage.Path)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			store, err := sqlite.OpenReadOnly(ctx, resolved)
			if err != nil {
				return fmt.Errorf("open store: %w (run `tokenops init`)", err)
			}
			defer func() { _ = store.Close() }()
			res, err := headroom.Advise(ctx, localHeadroomDeps(&cfg, store), headroom.AdviceQuery{Days: days, Project: true}, time.Now().UTC())
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res.Payload())
			}
			switch {
			case res.Unconfigured != "":
				return errors.New(res.Unconfigured)
			case res.StorageDisabled != "":
				return errors.New(res.StorageDisabled)
			}
			return writePlanAdvice(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().IntVar(&days, "days", headroom.DefaultAdviceDays, "how far back to replay, 1 to 90 (30 or 60 are the usual choices)")
	cmd.Flags().StringVar(&dbPath, "db", "", "event store path (defaults to the configured one)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the same JSON as GET /api/plans/advice")
	return cmd
}

// writePlanAdvice prints each provider's options as a table, the
// recommendation, and the caveats.
func writePlanAdvice(w io.Writer, res headroom.AdviceResult) error {
	for i, a := range res.Providers {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s: %s, replayed over the last %.0f days (%d windows), confidence %s\n",
			a.Provider, a.Display, a.Days, a.WindowsMeasured, a.Confidence)
		if line := valueLine(a); line != "" {
			fmt.Fprintf(w, "  %s\n", line)
		}
		if line := atResetLine(a.AtReset); line != "" {
			fmt.Fprintf(w, "  %s\n", line)
		}
		if len(a.Options) > 0 {
			fmt.Fprintln(w)
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "  OPTION\tLOCKOUTS\tBLOCKED\tPEAK\tPER MONTH\tSAVES")
			for _, o := range a.Options {
				name := o.Display
				if o.Current {
					name += " (now)"
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", name, lockoutsWords(a, o), blockedWords(o.BlockedMinutes),
					peakWords(o), moneyOrDash(o.MonthlyUSD), savingWords(o))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
		if r := a.Recommendation; r != nil {
			fmt.Fprintf(w, "\n  Recommendation: %s. %s\n", r.Verdict, r.Summary)
		}
		for _, c := range a.Caveats {
			fmt.Fprintf(w, "  - %s\n", c)
		}
	}
	for _, n := range res.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	if len(res.Providers) == 0 && len(res.Notes) == 0 {
		fmt.Fprintln(w, "No bound plan to replay.")
	}
	fmt.Fprintln(w, "\nAdvice only: TokenOps never switches a plan. Change it with the vendor; "+
		"TokenOps follows the plan your client then reports.")
	return nil
}

// valueLine says what the plan carried at API list price against what it
// cost for the same days; "" when either is not priced.
func valueLine(a headroom.Advice) string {
	if a.ListValueUSD == nil || a.ValueMultiple == nil {
		return ""
	}
	return fmt.Sprintf("Value: $%.2f of usage at API list price, %.1fx what the plan cost for those days "+
		"(a floor: only usage TokenOps sees counts).", *a.ListValueUSD, *a.ValueMultiple)
}

// atResetLine says where the plan's longest window is headed by its
// reset, in the words every surface uses for the forecast's rate.
func atResetLine(p *headroom.ResetProjection) string {
	if p == nil {
		return ""
	}
	when := p.ResetsAt.Local().Format("Mon 2 Jan 15:04")
	if p.TooEarly || p.AtResetPct == nil {
		return fmt.Sprintf("This %s: %.0f%% used; too early in the window to project to its reset on %s.", p.Window, p.UsedPct, when)
	}
	s := fmt.Sprintf("This %s: %.0f%% used; %s about %.0f%% by its reset on %s", p.Window, p.UsedPct,
		headroom.RateWords(p.Basis), *p.AtResetPct, when)
	if p.RunsOut {
		return s + ", so it runs out before then."
	}
	return s + "."
}

// lockoutsWords is an option's lockouts, or "-" when no window was read
// to count them.
func lockoutsWords(a headroom.Advice, o headroom.AdviceOption) string {
	if a.WindowsMeasured == 0 {
		return "-"
	}
	return fmt.Sprint(o.Lockouts)
}

func blockedWords(minutes int) string {
	switch {
	case minutes == 0:
		return "-"
	case minutes < 60:
		return fmt.Sprintf("%dm", minutes)
	case minutes < 48*60:
		return fmt.Sprintf("%.1fh", float64(minutes)/60)
	}
	return fmt.Sprintf("%.1fd", float64(minutes)/(24*60))
}

func peakWords(o headroom.AdviceOption) string {
	if o.PeakPct == nil || o.PeakWindow == "" {
		return "-"
	}
	return fmt.Sprintf("%.0f%% of %s", *o.PeakPct, o.PeakWindow)
}

func moneyOrDash(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("$%.2f", *v)
}

func savingWords(o headroom.AdviceOption) string {
	if o.Current || o.SavingsUSD == nil {
		return "-"
	}
	s := *o.SavingsUSD
	if math.Abs(s) < 0.005 {
		return "$0"
	}
	if s < 0 {
		return fmt.Sprintf("-$%.2f", -s)
	}
	return fmt.Sprintf("$%.2f", s)
}
