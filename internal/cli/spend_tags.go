package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/spending"
)

// runSpendByTag prints the window's spend per value of an allocation key:
// `tokenops spend --by project|client|cost_center`.
func runSpendByTag(cmd *cobra.Command, agg *spending.EventAggregator, f spending.Window, key allocation.Key,
	rules []allocation.Rule, jsonOut bool) error {
	until := f.Until
	if until.IsZero() {
		until = time.Now()
	}
	window := f
	window.Since, window.Until = time.Time{}, time.Time{}
	r, err := allocation.Compute(cmd.Context(), allocation.Deps{Turns: commits.TurnsIn(agg, window), Rules: rules},
		key, f.Since, until)
	if err != nil {
		return err
	}
	if jsonOut {
		return writeControlJSON(cmd, r)
	}
	writeTagReport(cmd.OutOrStdout(), r)
	return nil
}

func writeTagReport(out io.Writer, r allocation.Report) {
	fmt.Fprintf(out, "Spend by %s since %s\n", r.Key, r.Since.Local().Format("2 Jan 15:04"))
	if r.Rules == 0 {
		fmt.Fprintln(out, "\n  No allocation rules are configured, so nothing is tagged. Add rules under")
		fmt.Fprintln(out, "  allocation.rules in config.yaml, matching a repo, dir, branch_prefix or harness.")
		return
	}
	fmt.Fprintf(out, "  %.0f%% of the window's work carries a %s\n\n", r.TaggedShare*100, r.Key)
	if len(r.Groups) > 0 {
		fmt.Fprintf(out, "  %-24s %9s %9s %9s %6s %9s %10s\n", "VALUE", "API EQUIV", "BILLED", "SESSIONS", "TURNS", "TOKENS", "CACHE READ")
		for _, g := range r.Groups {
			writeTagRow(out, clipText(g.Value, 24), g.Cost, fmt.Sprint(g.Sessions))
		}
	}
	if r.Untagged.Turns > 0 {
		writeTagRow(out, "(untagged)", r.Untagged, "")
		if r.UnknownDirectory.Turns > 0 {
			fmt.Fprintf(out, "    %s of it from sessions whose directory no client recorded\n", usd(r.UnknownDirectory.APIEquivalentUSD))
		}
	}
	fmt.Fprintf(out, "\nAt API prices; billed is what was charged beyond a plan; cache read is the share of input read from the prompt cache. Tagged by rule: %s.\n", r.Method)
}

func writeTagRow(out io.Writer, label string, c commits.Cost, sessions string) {
	equiv := usd(c.APIEquivalentUSD)
	if c.UnpricedTurns > 0 {
		equiv += "+"
	}
	fmt.Fprintf(out, "  %-24s %9s %9s %9s %6d %9s %10s\n", label, equiv, usd(c.CostUSD), sessions, c.Turns, humanTokens(c.Tokens),
		sharePct(c.CacheReadShare))
}

// allocationKey reads --by as an allocation key, when it is one.
func allocationKey(by string) (allocation.Key, bool) {
	k, err := allocation.ParseKey(by)
	return k, err == nil
}
