package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/spending"
)

// runSpendByCommit is `tokenops spend --by commit`: what each of the
// operator's commits in the window cost, and the work no commit followed.
func runSpendByCommit(cmd *cobra.Command, agg *spending.Engine, f spending.Filter, jsonOut bool) error {
	report, err := commits.Compute(cmd.Context(), commits.Deps{Turns: commits.TurnsIn(agg, f)}, f.Since)
	if err != nil {
		return err
	}
	if jsonOut {
		return writeControlJSON(cmd, report)
	}
	writeCommitReport(cmd.OutOrStdout(), report)
	return nil
}

func writeCommitReport(out io.Writer, r commits.Report) {
	fmt.Fprintf(out, "Cost per commit since %s\n", r.Since.Local().Format("2 Jan 15:04"))
	if len(r.Commits) == 0 {
		fmt.Fprintln(out, "\n  No commits of yours followed agent work in this window.")
	} else {
		fmt.Fprintf(out, "  %d commits · median %s · mean %s · %.0f%% of the window's work placed on a commit\n\n",
			len(r.Commits), usd(r.MedianUSD), usd(r.MeanUSD), r.AttributedShare*100)
		fmt.Fprintf(out, "  %-12s %-14s %-12s %9s %6s %8s  %s\n", "WHEN", "REPO", "COMMIT", "COST", "TURNS", "WORK", "SUBJECT")
		for _, c := range r.Commits {
			cost := usd(c.APIEquivalentUSD)
			if c.UnpricedTurns > 0 {
				cost += "+"
			}
			fmt.Fprintf(out, "  %-12s %-14s %-12s %9s %6d %8s  %s\n", c.At.Local().Format("2 Jan 15:04"), clipText(c.Repo, 14),
				c.SHA[:min(10, len(c.SHA))], cost, c.Turns, minutesWords(c.WorkMinutes), clipText(c.Subject, 48))
		}
	}
	fmt.Fprintln(out)
	for _, b := range []struct {
		label string
		c     commits.Cost
	}{
		{"not yet committed", r.Uncommitted},
		{"no commit within a day", r.NoCommitWithinADay},
		{"outside a repository", r.OutsideARepository},
		{"session directory unknown", r.UnknownDirectory},
	} {
		if b.c.Turns > 0 {
			fmt.Fprintf(out, "  %-26s %9s  %d turns\n", b.label+":", usd(b.c.APIEquivalentUSD), b.c.Turns)
		}
	}
	fmt.Fprintf(out, "\nAt API prices. Joined by %s.\n", r.Method)
}

func usd(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func minutesWords(m float64) string {
	switch {
	case m >= 120:
		return fmt.Sprintf("%.0fh", m/60)
	case m >= 1:
		return fmt.Sprintf("%.0fm", m)
	}
	return "<1m"
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
