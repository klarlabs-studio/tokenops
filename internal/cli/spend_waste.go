package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/spending"
)

// newSpendWasteCmd builds `tokenops spend waste`: the period's avoidable
// usage, class by class, in dollars and plan-window share.
func newSpendWasteCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath, sinceFlag string
		jsonOut, local    bool
	)
	cmd := &cobra.Command{
		Use:   "waste",
		Short: "Avoidable usage by waste class, in dollars and plan-window share, and the guardrail to switch on next",
		Long: `waste prices one taxonomy of avoidable usage over the period:

  - redundant re-reads: files read again, unchanged, that the read guard
    watched go ahead
  - cache-miss rebuilds: prompt cache written again for a prefix that was
    cached a turn earlier, split by cause: a model switch, a prompt prefix
    change after cache hits, or unexplained
  - idle cache rebuilds: the same rebuild after a break longer than the
    cache lifetime
  - over-tiered model: turns left on a model after the coach advised a
    cheaper one for that kind of work
  - retry loops and runaway growth, and oversized context: what the waste
    detector finds in each workflow; the size it reads oversized at is the
    one your own context bands measured, when they show quality falling
  - stopped turns: the turns an instruction ran before you stopped it
  - abandoned sessions: over for a day with no commit and no outcome

Each class names the setting or habit that prevents it, and how few
sessions carry most of its dollars.

Each token is counted in at most one class. Every figure is in
API-equivalent dollars and, with a flat-rate plan's window readings, as a
share of the plan's window. Beside it: what the coach's interventions
realized, and the guardrails ranked by how ready their evidence is.

The running daemon keeps the repositories' history between reports, so
waste asks it; with --local, --db or no daemon answering, it computes the
report here.`,
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
			if dbPath == "" && !local {
				var r avoidable.Report
				if askDaemon(ctx, cfg, "/api/spend/waste?"+sinceQuery(since), reportDaemonWait, &r) {
					if jsonOut {
						return writeControlJSON(cmd, r)
					}
					writeWasteReport(cmd.OutOrStdout(), r)
					return nil
				}
			}
			store, err := openStoreForRead(ctx, path)
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
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path); computes the report here")
	cmd.Flags().BoolVar(&local, "local", false, "compute the report here instead of asking the running daemon")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "start of the period (RFC3339 or duration such as 7d); default the last 7 days")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeWasteReport(out io.Writer, r avoidable.Report) {
	fmt.Fprintf(out, "Avoidable usage since %s\n\n", r.Since.Local().Format("2 Jan 15:04"))
	writeAmountRow(out, "used", r.Usage, "")
	writePrevented(out, r.Prevented)
	of := ""
	if r.Usage.Tokens > 0 {
		of = fmt.Sprintf("  (%.0f%% of usage)", 100*float64(r.Avoidable.Tokens)/float64(r.Usage.Tokens))
	}
	writeAmountRow(out, "found", r.Avoidable, of+" still avoidable")

	fmt.Fprintf(out, "\n  %-32s %8s %9s %7s %6s  %s\n", "CLASS", "TOKENS", "API $", "WINDOW", "TURNS", "GUARDRAIL")
	inferred := false
	for _, c := range r.Classes {
		guard := "none yet"
		if c.Guardrail != "" {
			guard = savings.CapabilityName(c.Guardrail)
		}
		name := c.Name
		if c.Inferred {
			name += " *"
			inferred = inferred || c.Tokens > 0
		}
		fmt.Fprintf(out, "  %-32s %8s %9s %7s %6d  %s\n", name, humanCount(c.Tokens), usd(c.APIEquivalentUSD),
			windowPct(c.WindowShare), c.Turns, guard)
		if c.Tokens > 0 {
			if cc := c.Concentration; cc != nil && cc.Sessions > 0 {
				fmt.Fprintf(out, "    %s make %.0f%% of it\n", sessionCount(cc.Top), cc.TopPct)
			}
			if c.Fix != "" {
				fmt.Fprintf(out, "    fix: %s\n", c.Fix)
			}
		}
		for _, k := range c.Causes {
			name := k.Name
			if k.TTL != "" {
				name = "idle gaps over " + k.TTL
			}
			fmt.Fprintf(out, "    %-30s %8s %9s %7s %6d\n", name, humanCount(k.Tokens), usd(k.APIEquivalentUSD),
				windowPct(k.WindowShare), k.Turns)
			writeLikely(out, k)
		}
	}
	if inferred {
		fmt.Fprintln(out, "\n  * inferred from no commit and no outcome, which research, a review or a question also")
		fmt.Fprintln(out, "    leave: shown, not counted as avoidable")
	}

	if cl := r.ContextLine; cl != nil {
		fmt.Fprintf(out, "\n  Context line %s, from your own bands: %s\n", humanCount(cl.Tokens), cl.Basis)
	}
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
	for _, m := range r.Prevented.Mechanisms {
		if m.Count > 0 && m.Basis != "" {
			fmt.Fprintf(out, "  prevented, %s: %s\n", m.Name, m.Basis)
		}
	}
	for _, c := range r.Classes {
		for _, k := range c.Causes {
			if k.LikelyMethod != "" {
				fmt.Fprintf(out, "  likely: %s\n", k.LikelyMethod)
			}
		}
	}
	if r.ShareMethod != "" {
		fmt.Fprintf(out, "  plan share: %s\n", r.ShareMethod)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
}

// writeLikely names, under a prompt-prefix-change row, the context-lint
// items that plausibly explain its rebuilds: at most three, most rebuilds
// first.
func writeLikely(out io.Writer, k avoidable.CauseCost) {
	for i, l := range k.Likely {
		if i == 3 {
			fmt.Fprintf(out, "      likely: and %d more (`tokenops coach lint-context`)\n", len(k.Likely)-3)
			return
		}
		fmt.Fprintf(out, "      likely: %s (%d of %d rebuilds)\n", l.Finding, l.Rebuilds, k.Turns)
	}
}

// writePrevented is what the mechanisms stopped: the total, then each
// mechanism that acted with its count and its dollars or why it is not
// priced. It is never added into the found row.
func writePrevented(out io.Writer, p avoidable.Prevented) {
	fmt.Fprintf(out, "  %-10s %s\n", "prevented", p.Words())
	for _, m := range p.Mechanisms {
		if m.Count == 0 {
			continue
		}
		price := "not priced"
		if v, ok := m.USD.Amount(); ok {
			price = usd(v)
		}
		fmt.Fprintf(out, "    %-34s %6d %10s\n", m.Name, m.Count, price)
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

// sessionCount is n sessions, singular for one.
func sessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}
