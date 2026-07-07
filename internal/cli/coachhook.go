package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

// stopHookInput is the JSON Claude Code sends a Stop hook on stdin. Only the
// fields the coach needs are decoded; unknown fields are ignored.
type stopHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// stopHookOutput is the JSON a Stop hook writes to stdout to surface a
// user-facing, non-blocking message. systemMessage is Claude Code's documented
// channel for a nudge the operator sees; it does NOT block or force the agent
// to continue (which decision:"block" would). suppressOutput keeps the hook's
// own stdout out of the transcript.
type stopHookOutput struct {
	SystemMessage  string `json:"systemMessage"`
	SuppressOutput bool   `json:"suppressOutput"`
}

// newCoachHookCmd is the Claude Code Stop-hook coaching nudge. Its bare form is
// the hook handler (reads the Stop JSON on stdin, accumulates the session's
// API-equivalent spend, and emits a graduated systemMessage nudge as that spend
// crosses fractions of a per-session budget); the `hook` and `stats`
// subcommands install and inspect it.
func newCoachHookCmd() *cobra.Command {
	var (
		budget float64
		dir    string
	)
	cmd := &cobra.Command{
		Use:   "coach-hook",
		Short: "Claude Code Stop hook that nudges as a session's cumulative cost crosses budget fractions",
		Long: `coach-hook is a Claude Code Stop hook. Wired onto Stop, it reads the
tail of the session transcript after each turn, sums the full API-equivalent
cost of the new turns — cache-read is the dominant, most reclaimable part, and
it compounds every turn you carry a large context — and, as the session's
cumulative spend crosses fractions of a per-session budget (default $50),
surfaces graduated, non-blocking nudges to /compact or start a fresh session. It
works for clients that never route through the tokenops proxy (e.g. Claude Code
on a subscription), because it acts inside the client.

Unlike a flat per-turn threshold, a cumulative budget catches the long, flat
sessions where no single turn looks extreme but thousands of turns compound into
real money. Each budget-fraction alert (50%, 75%, 100%, then every additional
budget over) fires once, so the coach never nags every turn.

Bare invocation is the hook handler (reads Stop JSON on stdin). Use
'tokenops coach-hook hook' to print the settings.json block (or prefer
'tokenops hooks install --coach'), and 'tokenops coach-hook stats' to see how
much your sessions have spent and which budget alerts fired.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := coachhook.DefaultConfig()
			cfg.BudgetUSD = budget
			return runCoachHook(cmd, dir, cfg)
		},
	}
	cmd.Flags().Float64Var(&budget, "budget", coachhook.DefaultBudgetUSD, "per-session API-equivalent USD budget the alert fractions measure against")
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/coach-hook)")
	cmd.AddCommand(newCoachHookHookCmd())
	cmd.AddCommand(newCoachHookStatsCmd())
	return cmd
}

// runCoachHook reads the Stop JSON, evaluates, and emits a nudge if warranted.
// Errors never disrupt the session — a coach must fail open. On nudge it writes
// {systemMessage, suppressOutput:true}; on no-nudge or any error it exits 0
// with no stdout.
func runCoachHook(cmd *cobra.Command, dir string, cfg coachhook.Config) error {
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil // fail open
	}
	var in stopHookInput
	if err := json.Unmarshal(body, &in); err != nil {
		return nil // fail open
	}
	dec := coachhook.Evaluate(dir, in.SessionID, in.TranscriptPath, cfg, time.Now())
	if !dec.Nudge {
		return nil // no nudge: exit 0 with no stdout
	}
	out := stopHookOutput{SystemMessage: dec.Message, SuppressOutput: true}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

func newCoachHookHookCmd() *cobra.Command {
	var budget float64
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Print the settings.json block to wire coach-hook into Claude Code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe := "tokenops"
			if p, err := os.Executable(); err == nil {
				exe = p
			}
			block := map[string]any{
				"hooks": map[string]any{
					"Stop": []any{
						map[string]any{
							"hooks": []any{
								map[string]any{
									"type":    "command",
									"command": exe,
									"args":    []string{"coach-hook", "--budget", formatBudget(budget)},
									"timeout": 10,
								},
							},
						},
					},
				},
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			fmt.Fprintf(cmd.ErrOrStderr(), "# Add this to ~/.claude/settings.json (or run `tokenops hooks install --coach`).\n")
			return enc.Encode(block)
		},
	}
	cmd.Flags().Float64Var(&budget, "budget", coachhook.DefaultBudgetUSD, "per-session API-equivalent USD budget")
	return cmd
}

func newCoachHookStatsCmd() *cobra.Command {
	var (
		dir     string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show coach-hook session spend (budget alerts fired, max/total est $)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := coachhook.ReadStats(dir)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}
			out := cmd.OutOrStdout()
			if s.Events == 0 {
				fmt.Fprintln(out, "No coach-hook activity yet. Install the hook (`tokenops hooks install --coach`) and use Claude Code.")
				return nil
			}
			fmt.Fprintf(out, "coach-hook — %d Stop events across %d sessions\n", s.Events, s.DistinctSessions)
			fmt.Fprintf(out, "  est. API-equiv spend: max session ~$%.2f · total ~$%.2f\n", s.MaxCumulativeUSD, s.TotalEstSpendUSD)
			fmt.Fprintf(out, "  budget alerts fired: %d\n", s.Alerts)
			for _, tier := range []string{"50%", "75%", "100%", "200%", "300%"} {
				if n := s.AlertsByTier[tier]; n > 0 {
					fmt.Fprintf(out, "    %-5s %d\n", tier, n)
				}
			}
			if s.Alerts == 0 {
				fmt.Fprintln(out, "\nNo session has crossed a budget fraction yet — your spend stays lean. Keep observing.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/coach-hook)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

// formatBudget renders a budget for the hook args: whole dollars without a
// trailing ".0" ("50"), otherwise the decimal form ("49.5").
func formatBudget(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
