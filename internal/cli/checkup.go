package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/checkup"
)

// newCheckupCmd is the one-shot look at a week of agent work. It runs on
// a fresh machine with no config and no daemon, reading the clients' own
// transcripts, so it is the first thing to run, before `init`.
func newCheckupCmd() *cobra.Command {
	var (
		days    int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "checkup",
		Short: "A week of agent work in one read: what it cost, where it leaked, what fixes it",
		Long: `checkup reads what your coding agents already wrote down — Claude Code,
Codex, Gemini CLI, opencode — and reports the last week: tokens and their
value at API prices per harness and model, how the sessions went (graded),
and each leak it finds with the one command that fixes it:

  re-reads       files read again with no edit in between
  instructions   CLAUDE.md and AGENTS.md re-read on every turn
  model fit      lookups answered on a flagship model

It needs no config, no daemon and no store, and sends nothing anywhere.
Instruction text is read in memory to classify it and never written.`,
		Example: `  tokenops checkup
  tokenops checkup --days 30
  tokenops checkup --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			var progress *activity
			if !jsonOut {
				progress = startActivity(cmd.ErrOrStderr(), "Reading a week of transcripts")
			}
			r := checkup.Compute(cmd.Context(), checkup.Options{Home: home, Days: days, Now: time.Now(), Installed: installedHooks()})
			if progress != nil {
				progress.success("read")
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(r)
			}
			renderCheckup(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, "how many days back to read")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the report as JSON")
	return cmd
}

// installedHooks names the TokenOps hooks Claude Code's settings wire.
// Unreadable settings mean none: the findings then say how to install.
func installedHooks() map[string]bool {
	out := map[string]bool{}
	st, err := hooksStatusOf("claude-code", "", selfExe())
	if err != nil {
		return out
	}
	for _, h := range st.Hooks {
		out[h.Hook] = true
	}
	return out
}

func renderCheckup(w io.Writer, r checkup.Report) {
	fmt.Fprintf(w, "TokenOps checkup — %s\n\n", r.Window)
	if r.Total.Turns == 0 {
		fmt.Fprintln(w, "No agent turns found in this window. TokenOps reads Claude Code, Codex, Gemini CLI and opencode where they keep their records.")
		return
	}
	fmt.Fprintf(w, "%-12s %-28s %8s %10s %12s\n", "HARNESS", "MODEL", "TURNS", "TOKENS", "API VALUE")
	for _, u := range r.Usage {
		fmt.Fprintf(w, "%-12s %-28s %8d %10s %12s\n", clipTo(u.Harness, 12), clipTo(u.Model, 28), u.Turns,
			compactTokens(u.InputTokens+u.OutputTokens), usdOrUnpriced(u))
	}
	t := r.Total
	fmt.Fprintf(w, "%-41s %8d %10s %12s\n", "Total", t.Turns, compactTokens(t.InputTokens+t.OutputTokens), usdOrUnpriced(t))
	fmt.Fprintln(w, "API value is what the tokens would cost on an API key; on a flat-rate plan it is a measure, not a bill.")

	if g := r.DX.Grades.Overall; g != "" {
		fmt.Fprintf(w, "\nSessions: %s  (%d instructions; `tokenops dx` for the breakdown)\n", g, r.DX.Metrics.Prompts)
	}
	fmt.Fprintln(w)
	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "No leaks found.")
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "%s %s\n  %s\n  fix: %s\n\n", levelMark(f.Level), f.Title, f.Evidence, f.Fix)
	}
	for _, warn := range r.Warnings {
		fmt.Fprintf(w, "note: %s\n", warn)
	}
	fmt.Fprintln(w, "`tokenops init` sets up the coach, which acts on these as they happen.")
}

func levelMark(l checkup.Level) string {
	switch l {
	case checkup.LevelWarn:
		return "▲"
	case checkup.LevelNotice:
		return "●"
	}
	return "·"
}

func usdOrUnpriced(u checkup.Usage) string {
	if u.CostUSD == 0 && u.UnpricedTurns > 0 {
		return "unpriced"
	}
	s := fmt.Sprintf("$%.2f", u.CostUSD)
	if u.UnpricedTurns > 0 {
		s += "+"
	}
	return s
}

func compactTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func clipTo(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
