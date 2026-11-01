package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/claudecodeorg"
)

// newSpendClaudeCodeCmd builds `tokenops spend claude-code`: the
// organisation's Claude Code use as Anthropic reports it, read with a
// Claude Console admin key.
func newSpendClaudeCodeCmd(rf *rootFlags) *cobra.Command {
	var (
		days    int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "claude-code",
		Short: "The organisation's Claude Code use as Anthropic reports it (needs an admin or Enterprise Analytics key)",
		Long: `claude-code reads Anthropic's own Claude Code analytics for your
organisation, one UTC day at a time: users, sessions, lines added and
removed, commits and pull requests Claude Code made, and how many of its
edits were accepted. Anthropic reports it per user; TokenOps sums it and
keeps no names or addresses. It reads every report a configured key
reaches:

  console     vendor_usage.anthropic.admin_key (TOKENOPS_ANTHROPIC_ADMIN_KEY),
              a Claude Console admin key: Claude Code billed through the
              Console organisation, with tokens and Anthropic's estimated
              cost by model.
  enterprise  vendor_usage.anthropic.analytics_key
              (TOKENOPS_ANTHROPIC_ANALYTICS_KEY), a Claude Enterprise
              Analytics API key the primary owner creates in claude.ai:
              Claude Code by the organisation's claude.ai sign-ins. No cost
              in this report, and about a day behind; days not published
              yet are listed as pending.

Pro, Max and Team subscriptions have no such report; TokenOps reads
those from each machine.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			a := cfg.VendorUsage.Anthropic
			res, err := claudecodeorg.ReadAll(cmd.Context(), claudecodeorg.Sources(a.AdminKey, a.AnalyticsKey), time.Now(), days)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, res)
			}
			for i, rep := range res.Reports {
				if i > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				writeClaudeCodeReport(cmd.OutOrStdout(), rep)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, fmt.Sprintf("UTC days to read, up to and including today (1 to %d)", claudecodeorg.MaxDays))
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeClaudeCodeReport(out io.Writer, r claudecodeorg.Report) {
	title := map[string]string{claudecodeorg.SourceConsole: "your Claude Console organisation",
		claudecodeorg.SourceEnterprise: "your Claude Enterprise organisation"}[r.Source]
	fmt.Fprintf(out, "Claude Code in %s, %s to %s (UTC)\n\n", title, r.Since, r.Until)
	fmt.Fprintf(out, "  %-10s %5s %8s %7s %4s %9s %9s %9s %8s %9s\n",
		"DAY", "USERS", "SESSIONS", "COMMITS", "PRS", "+LINES", "-LINES", "ACCEPTED", "TOKENS", "EST. $")
	row := func(label string, d claudecodeorg.Day) {
		acc := "—"
		if pct, ok := d.Decisions().AcceptancePct(); ok {
			acc = fmt.Sprintf("%.0f%%", pct)
		}
		tokens, cost := "—", "—"
		if len(d.Models) > 0 {
			tokens, cost = humanCount(d.Tokens()), usd(d.EstimatedUSD())
		}
		fmt.Fprintf(out, "  %-10s %5d %8d %7d %4d %9d %9d %9s %8s %9s\n", label, d.Users+d.APIKeys, d.Sessions, d.Commits,
			d.PullRequests, d.LinesAdded, d.LinesRemoved, acc, tokens, cost)
	}
	for _, d := range r.Days {
		row(d.Day, d)
	}
	row("total", r.Total)
	if len(r.Pending) > 0 {
		fmt.Fprintf(out, "  Not published yet: %s.\n", strings.Join(r.Pending, ", "))
	}
	fmt.Fprintf(out, "\n  USERS counts users and API keys each day; the total counts user-days.\n")
	fmt.Fprintf(out, "  Scope: %s.\n", r.Scope)
}
