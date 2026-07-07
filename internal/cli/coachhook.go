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
// the hook handler (reads the Stop JSON on stdin, evaluates cache-read load,
// emits a systemMessage nudge when a session is carrying too much reclaimable
// context); the `hook` and `stats` subcommands install and inspect it.
func newCoachHookCmd() *cobra.Command {
	var (
		threshold int64
		cooldown  int
		dir       string
	)
	cmd := &cobra.Command{
		Use:   "coach-hook",
		Short: "Claude Code Stop hook that nudges when a session carries too much cache-read context",
		Long: `coach-hook is a Claude Code Stop hook. Wired onto Stop, it reads the
tail of the session transcript after each turn, measures how many cache-read
tokens the session is carrying per turn — the dominant, most reclaimable cost
in a long session, re-billed every turn until you compact — and, when that
crosses a threshold, surfaces a non-blocking nudge to /compact or start a fresh
session. It works for clients that never route through the tokenops proxy (e.g.
Claude Code on a subscription), because it acts inside the client.

The coach never blocks and never forces the agent to keep going: it emits a
systemMessage the operator sees and otherwise stays silent. A per-session
cooldown keeps it from nagging every turn.

Bare invocation is the hook handler (reads Stop JSON on stdin). Use
'tokenops coach-hook hook' to print the settings.json block (or prefer
'tokenops hooks install --coach'), and 'tokenops coach-hook stats' to see how
much cache-read load your sessions have been carrying.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := coachhook.DefaultConfig()
			cfg.CacheReadThreshold = threshold
			cfg.CooldownTurns = cooldown
			return runCoachHook(cmd, dir, cfg)
		},
	}
	cmd.Flags().Int64Var(&threshold, "threshold", coachhook.DefaultCacheReadThreshold, "cache-read tokens/turn at/above which to nudge")
	cmd.Flags().IntVar(&cooldown, "cooldown", coachhook.DefaultCooldownTurns, "turns to wait between nudges for a session")
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
	var (
		threshold int64
		cooldown  int
	)
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
									"args":    []string{"coach-hook", "--threshold", strconv.FormatInt(threshold, 10), "--cooldown", strconv.Itoa(cooldown)},
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
	cmd.Flags().Int64Var(&threshold, "threshold", coachhook.DefaultCacheReadThreshold, "cache-read tokens/turn at/above which to nudge")
	cmd.Flags().IntVar(&cooldown, "cooldown", coachhook.DefaultCooldownTurns, "turns to wait between nudges for a session")
	return cmd
}

func newCoachHookStatsCmd() *cobra.Command {
	var (
		dir     string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show coach-hook cache-read load (nudges, max/avg tokens/turn, reclaimable $)",
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
			fmt.Fprintf(out, "coach-hook — %d turns seen across %d sessions\n", s.Events, s.DistinctSessions)
			fmt.Fprintf(out, "  cache-read carried per turn: max ~%s · avg ~%s tokens\n", humanTokens(s.MaxCacheReadPerTurn), humanTokens(s.AvgCacheReadPerTurn))
			fmt.Fprintf(out, "  nudges surfaced: %d\n", s.Nudges)
			if s.EstReclaimableUSD > 0 {
				fmt.Fprintf(out, "  est. API-equiv reclaimable on nudged turns: ~$%.2f\n", s.EstReclaimableUSD)
			}
			if s.Nudges == 0 {
				fmt.Fprintln(out, "\nNo session has crossed the nudge threshold yet — your context stays lean. Keep observing.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/coach-hook)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
