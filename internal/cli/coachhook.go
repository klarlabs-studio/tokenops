package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/config"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
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
func newCoachHookCmd(rf *rootFlags) *cobra.Command {
	var (
		budget   float64
		dir      string
		guardDir string
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
			cfg.Enabled = advisoryCoaching(rf)
			cfg.Quiet = quietPolicy(rf)
			cfg.Promotion = promotionNudge(rf, guardDir)
			cfg.Rates = datedRates(rf)
			return runCoachHook(cmd, dir, cfg)
		},
	}
	cmd.Flags().Float64Var(&budget, "budget", coachhook.DefaultBudgetUSD, "per-session API-equivalent USD budget the alert fractions measure against")
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/coach-hook)")
	cmd.Flags().StringVar(&guardDir, "guard-dir", "", "read-guard ledger dir to read the promotion case from (defaults to ~/.tokenops/read-guard)")
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
			if len(s.UnpricedModels) > 0 {
				fmt.Fprintln(out, "  not priced — no rate card for these models:")
				for _, m := range sortedKeys(s.UnpricedModels) {
					fmt.Fprintf(out, "    %-22s %d turn(s)\n", m, s.UnpricedModels[m])
				}
				fmt.Fprintln(out, "    their spend reads as $0 above. Add rates via `pricing.path` to count them.")
			}
			if s.PromotionNudges > 0 {
				fmt.Fprintf(out, "  read-guard case argued: %d session(s)\n", s.PromotionNudges)
			}
			if len(s.Suppressed) > 0 {
				fmt.Fprintf(out, "  held back by coaching.quiet: %d\n", totalOf(s.Suppressed))
				for _, rule := range []string{"min_interval", "max_per_session"} {
					if n := s.Suppressed[rule]; n > 0 {
						fmt.Fprintf(out, "    %-15s %d\n", rule, n)
					}
				}
			}
			switch {
			case s.Alerts == 0 && len(s.UnpricedModels) > 0:
				// "Lean" would be an over-claim: nothing crossed a budget
				// fraction because nothing could be measured against one.
				fmt.Fprintln(out, "\nNo session has crossed a budget fraction — but some turns could not be priced,")
				fmt.Fprintln(out, "so this is not evidence that your spend is lean. Price those models first.")
			case s.Alerts == 0:
				fmt.Fprintln(out, "\nNo session has crossed a budget fraction yet — your spend stays lean. Keep observing.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/coach-hook)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func totalOf(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// formatBudget renders a budget for the hook args: whole dollars without a
// trailing ".0" ("50"), otherwise the decimal form ("49.5").
func formatBudget(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// advisoryCoaching reports whether the coach may nudge unprompted. The
// nudge never blocks, so it sits on the `advise` rung, which is the
// default — installing the Stop hook is itself the operator asking for
// it, and an upgrade must not quietly take that away.
//
// At `observe` the hook still runs and still records the ledger; it just
// says nothing, which is what makes `coach-hook stats` meaningful before
// you let it speak.
//
// An unreadable config falls back to the default rather than to silence.
// A coach that goes quiet because it could not parse a YAML file is the
// silent-failure shape this tool exists to find.
func advisoryCoaching(rf *rootFlags) bool {
	cfg, err := loadConfig(rf)
	if err != nil {
		return config.CoachingConfig{}.AllowsAdvice()
	}
	return cfg.Coaching.AllowsAdvice()
}

// promotionNudge builds the case for letting the read guard start
// blocking, or returns empty when nobody should hear it.
//
// Who pulls the trigger follows `coaching.delivery` itself rather than a
// second knob, because it is the same question that ladder already
// answers:
//
//	observe    — record the evidence, say nothing
//	advise     — make the case, wait for the human
//	intervene  — already there
//
// So `advise` is the only level that argues. At `observe` the coach is
// silent by construction; at `intervene` the guard is already refusing
// re-reads and there is nothing left to ask for.
func promotionNudge(rf *rootFlags, guardDir string) string {
	cfg, err := loadConfig(rf)
	if err != nil {
		// An unreadable config resolves to advise elsewhere in this file,
		// but silence is the right failure here: the case is a standing
		// one that will be just as true after the config is fixed, and
		// arguing for an intervention on a config we could not read is
		// the wrong direction to fail in.
		return ""
	}
	if cfg.Coaching.DeliveryLevel() != config.DeliveryAdvise {
		return ""
	}
	stats, err := readguard.ReadStats(guardDir)
	if err != nil {
		return ""
	}
	return promotionCase(stats)
}

// datedRates resolves the rate card the rest of tokenops prices with:
// the embedded baseline, plus the snapshots under ~/.tokenops/pricing,
// plus any negotiated-rate override — effective-dated, so a turn is
// priced at the card in force when it ran.
//
// This package used the embedded baseline alone until it turned out that
// is not the same card. A machine whose snapshot knew gpt-5.5 still had
// its coach-hook budget measured against a baseline that did not, so no
// tier could fire and the session read as free.
//
// Nil on any failure, which falls back to the baseline: a coach that
// cannot price is still better than a hook that refuses to run.
func datedRates(rf *rootFlags) func(time.Time) spend.Table {
	path := ""
	if cfg, err := loadConfig(rf); err == nil {
		path = cfg.Pricing.Path
	}
	overrides := spend.Table{}
	if path != "" {
		if ov, err := spend.LoadTableFile(path); err == nil {
			overrides = ov
		}
	}
	eng, err := pricing.EffectiveEngineWithOverrides("", overrides)
	if err != nil || eng == nil {
		return nil
	}
	return eng.TableAt
}

// quietPolicy reads coaching.quiet into the hook's rate limit.
//
// An unreadable config yields the zero policy — no floor, no cap — for
// the same reason advisoryCoaching falls back to speaking: a coach that
// silences itself because it could not parse a YAML file is the
// silent-failure shape this tool exists to find. The per-finding latches
// still apply, so the fallback is the behaviour that shipped before the
// key existed, not an unbounded one.
func quietPolicy(rf *rootFlags) coachhook.Quiet {
	cfg, err := loadConfig(rf)
	if err != nil {
		return coachhook.Quiet{}
	}
	return coachhook.Quiet{
		MinInterval:   cfg.Coaching.Quiet.MinInterval,
		MaxPerSession: cfg.Coaching.Quiet.MaxPerSession,
	}
}
