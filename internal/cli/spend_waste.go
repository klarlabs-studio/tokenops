package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// newSpendWasteCmd builds `tokenops spend waste`: the period's avoidable
// usage, class by class, in dollars and plan-window share.
func newSpendWasteCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath, sinceFlag string
		jsonOut           bool
	)
	cmd := &cobra.Command{
		Use:   "waste",
		Short: "Avoidable usage by waste class, in dollars and plan-window share, and the guardrail to switch on next",
		Long: `waste prices one taxonomy of avoidable usage over the period:

  - redundant re-reads: files read again, unchanged, that the read guard
    watched go ahead
  - cache-miss rebuilds: prompt cache written again for a prefix that was
    cached a turn earlier, split by cause: a model switch, an idle gap past
    the cache lifetime, a prompt prefix change after cache hits, or
    unexplained
  - over-tiered model: turns left on a model after the coach advised a
    cheaper one for that kind of work
  - retry loops and runaway growth, and oversized context: what the waste
    detector finds in each workflow
  - abandoned sessions: over for a day with no commit and no outcome

Each token is counted in at most one class. Every figure is in
API-equivalent dollars and, with a flat-rate plan's window readings, as a
share of the plan's window. Beside it: what the coach's interventions
realized, and the guardrails ranked by how ready their evidence is.`,
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
			since := now.AddDate(0, 0, -avoidable.DefaultDays)
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
			r, err := avoidable.Compute(ctx, avoidable.DepsFor(cfg, spending.NewAggregator(store, eng), store, eng), since, now)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, r)
			}
			writeWasteReport(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "start of the period (RFC3339 or duration such as 7d); default the last 7 days")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeWasteReport(out io.Writer, r avoidable.Report) {
	fmt.Fprintf(out, "Avoidable usage since %s\n\n", r.Since.Local().Format("2 Jan 15:04"))
	writeAmountRow(out, "used", r.Usage, "")
	of := ""
	if r.Usage.Tokens > 0 {
		of = fmt.Sprintf("  (%.0f%% of usage)", 100*float64(r.Avoidable.Tokens)/float64(r.Usage.Tokens))
	}
	writeAmountRow(out, "avoidable", r.Avoidable, of)

	fmt.Fprintf(out, "\n  %-32s %8s %9s %7s %6s  %s\n", "CLASS", "TOKENS", "API $", "WINDOW", "TURNS", "GUARDRAIL")
	for _, c := range r.Classes {
		guard := "none yet"
		if c.Guardrail != "" {
			guard = savings.CapabilityName(c.Guardrail)
		}
		fmt.Fprintf(out, "  %-32s %8s %9s %7s %6d  %s\n", c.Name, humanCount(c.Tokens), usd(c.APIEquivalentUSD),
			windowPct(c.WindowShare), c.Turns, guard)
		for _, k := range c.Causes {
			name := k.Name
			if k.TTL != "" {
				name += " (" + k.TTL + ")"
			}
			fmt.Fprintf(out, "    %-30s %8s %9s %7s %6d\n", name, humanCount(k.Tokens), usd(k.APIEquivalentUSD),
				windowPct(k.WindowShare), k.Turns)
		}
	}

	rz := r.Realized
	fmt.Fprintf(out, "\n  Realized %s", usd(rz.USD))
	var parts []string
	if rz.RefusedRereads > 0 {
		parts = append(parts, fmt.Sprintf("%d re-reads refused, %s tokens", rz.RefusedRereads, humanCount(rz.RefusedTokens)))
	}
	if v, ok := rz.HoldoutUSD.Amount(); ok {
		parts = append(parts, fmt.Sprintf("%s measured against the holdout", usd(v)))
	} else {
		parts = append(parts, "holdout: "+rz.HoldoutUSD.Caveat())
	}
	for i, p := range parts {
		sep := "; "
		if i == 0 {
			sep = " ("
		}
		fmt.Fprint(out, sep+p)
	}
	fmt.Fprint(out, ")")
	if rz.Share != nil {
		fmt.Fprintf(out, ": %.0f%% of what could be saved", 100**rz.Share)
	}
	fmt.Fprintln(out)

	if n := r.Next; n != nil {
		state := "still gathering evidence"
		if n.Ready {
			state = fmt.Sprintf("ready to %s", n.Next)
		}
		fmt.Fprintf(out, "\n  Next guardrail: %s, %s, addresses %s", n.Name, state, usd(n.APIEquivalentUSD))
		if n.Ready && n.Promote != "" {
			fmt.Fprintf(out, " — `%s` takes the step", n.Promote)
		}
		fmt.Fprintln(out)
	}
	if len(r.Guardrails) > 0 {
		fmt.Fprintln(out, "\n  Guardrails, ready first, then by what each addresses")
		for _, g := range r.Guardrails {
			stage := string(g.Stage)
			if g.Next != "" {
				stage += " → " + string(g.Next)
			} else {
				stage += " (acting)"
			}
			fmt.Fprintf(out, "  %-16s %-18s %9s\n", g.Name, stage, usd(g.APIEquivalentUSD))
			if g.Bar != "" {
				fmt.Fprintf(out, "      %s\n", g.Bar)
			}
		}
	}

	fmt.Fprintf(out, "\nAt API prices; %s.\n", r.Attribution)
	for _, c := range r.Classes {
		if c.Tokens > 0 {
			fmt.Fprintf(out, "  %s: %s\n", c.Name, c.Method)
		}
	}
	if r.ShareMethod != "" {
		fmt.Fprintf(out, "  plan share: %s\n", r.ShareMethod)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
}

func writeAmountRow(out io.Writer, label string, a avoidable.Amount, tail string) {
	fmt.Fprintf(out, "  %-10s %8s tokens %9s", label, humanCount(a.Tokens), usd(a.APIEquivalentUSD))
	for _, s := range a.WindowShare {
		fmt.Fprintf(out, "   %.1f%% of the %s %s window", s.Pct, s.Provider, s.Window)
	}
	fmt.Fprintln(out, tail)
}

// windowPct is the largest window share, or a dash without one.
func windowPct(ss []avoidable.WindowShare) string {
	best := -1.0
	for _, s := range ss {
		best = max(best, s.Pct)
	}
	if best < 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", best)
}

// humanCount is a token count at a glance.
func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
