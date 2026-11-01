package cli

import (
	"errors"
	"fmt"
	"io"
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
		Short: "The organisation's Claude Code use as Anthropic reports it (needs a Claude Console admin key)",
		Long: `claude-code reads Anthropic's Claude Code analytics for your Claude
Console organisation, one UTC day at a time: users, sessions, lines added
and removed, commits and pull requests Claude Code made, how many of its
edits were accepted, tokens, and Anthropic's estimated cost by model.

Anthropic reports it per user; TokenOps sums it and keeps no names or
addresses. It covers Claude Code billed through the Console organisation.
Sign-ins with a Claude subscription (Pro, Max, Team, Enterprise) are
reported under claude.ai and are not in it; TokenOps reads those from
each machine instead.

The admin key is vendor_usage.anthropic.admin_key or
TOKENOPS_ANTHROPIC_ADMIN_KEY (sk-ant-admin-..., minted in the Claude
Console under Settings, Admin keys).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			key := cfg.VendorUsage.Anthropic.AdminKey
			if key == "" {
				return errors.New("no Anthropic admin key: set vendor_usage.anthropic.admin_key or TOKENOPS_ANTHROPIC_ADMIN_KEY (sk-ant-admin-..., from the Claude Console)")
			}
			rep, err := claudecodeorg.Read(cmd.Context(), claudecodeorg.Reporter(key), time.Now(), days)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, rep)
			}
			writeClaudeCodeReport(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, fmt.Sprintf("UTC days to read, up to and including today (1 to %d)", claudecodeorg.MaxDays))
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeClaudeCodeReport(out io.Writer, r claudecodeorg.Report) {
	fmt.Fprintf(out, "Claude Code in your Console organisation, %s to %s (UTC)\n\n", r.Since, r.Until)
	fmt.Fprintf(out, "  %-10s %5s %8s %7s %4s %9s %9s %9s %8s %9s\n",
		"DAY", "USERS", "SESSIONS", "COMMITS", "PRS", "+LINES", "-LINES", "ACCEPTED", "TOKENS", "EST. $")
	row := func(label string, d claudecodeorg.Day) {
		acc := "—"
		if pct, ok := d.Decisions().AcceptancePct(); ok {
			acc = fmt.Sprintf("%.0f%%", pct)
		}
		fmt.Fprintf(out, "  %-10s %5d %8d %7d %4d %9d %9d %9s %8s %9s\n", label, d.Users+d.APIKeys, d.Sessions, d.Commits,
			d.PullRequests, d.LinesAdded, d.LinesRemoved, acc, humanCount(d.Tokens()), usd(d.EstimatedUSD()))
	}
	for _, d := range r.Days {
		row(d.Day, d)
	}
	row("total", r.Total)
	fmt.Fprintf(out, "\n  USERS counts users and API keys each day; the total counts user-days.\n")
	fmt.Fprintf(out, "  Scope: %s.\n", r.Scope)
}
