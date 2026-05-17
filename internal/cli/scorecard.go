package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/prompts"
	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/tools"
	"github.com/felixgeelhaar/tokenops/internal/contexts/governance/scorecard"
)

func newScorecardCmd() *cobra.Command {
	var jsonOut bool
	var baselineRef string
	var dbPath string
	var sinceDays int
	var fvtOverride float64
	var teuOverride float64
	var sacOverride float64

	cmd := &cobra.Command{
		Use:   "scorecard",
		Short: "Show the operator wedge KPI scorecard",
		Long: `scorecard computes and displays the operator wedge KPI scorecard,
which measures three key outcomes:

  First-Value Time (FVT)      — seconds from daemon start to first
                                observable result (proxy, spend, event).
                                Lower is better. Threshold: ≤60s green.

  Token Efficiency Uplift     — percentage reduction in tokens when the
    (TEU)                       optimizer pipeline is active vs. passive.
                                Higher is better. Threshold: ≥20% green.

  Spend Attribution           — percentage of total spend associated
    Completeness (SAC)          with a known workflow, agent, or session.
                                Higher is better. Threshold: ≥90% green.

The scorecard aggregates these three into an overall grade (A–F).

Use --capture-baseline (not yet implemented) to persist the current
values and --compare to diff against a stored baseline.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agent := computeAgentKPIs(sinceDays)
			s := scorecard.Build(cmd.Context(), scorecard.BuildParams{
				DBPath:             dbPath,
				SinceDays:          sinceDays,
				FVTSecondsOverride: fvtOverride,
				TEUPctOverride:     teuOverride,
				SACPctOverride:     sacOverride,
				AgentKPIs:          agent,
				BaselineRef:        baselineRef,
			})

			if jsonOut {
				data, err := s.MarshalJSON()
				if err != nil {
					return fmt.Errorf("marshal scorecard: %w", err)
				}
				cmd.Println(string(data))
				return nil
			}
			cmd.Print(s.String())
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&baselineRef, "baseline-ref", "", "reference identifier for the baseline (version, date, or label)")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to ~/.tokenops/events.db)")
	cmd.Flags().IntVar(&sinceDays, "since-days", 7, "scorecard time window in days")
	cmd.Flags().Float64Var(&fvtOverride, "fvt-seconds", 0, "override First-Value Time in seconds")
	cmd.Flags().Float64Var(&teuOverride, "teu-pct", 0, "override Token Efficiency Uplift in percent")
	cmd.Flags().Float64Var(&sacOverride, "sac-pct", 0, "override Spend Attribution Completeness in percent")
	return cmd
}

// computeAgentKPIs derives the v0.19 agent KPIs that the scorecard
// package can't compute from events.db alone (CGR + RGR need prompt
// text). Walks both Claude Code + Codex JSONL roots via the prompts
// extractor, runs Analyze, and packs the ratios into AgentKPIInputs.
// Returns a zero-valued struct (no *Computed flags set) when the
// extractor can't find any prompts — Build then falls through to
// the in-store CHR computation only.
//
// CGR autonomous-loop filter: `continue`, `proceed`, `keep going`
// appearing >5x in the same session are autonomous-loop sentinels
// (the /loop dynamic-mode pacing) rather than real human acks.
// Counting them in CGR penalizes the operator's autonomous
// workflows. Strip them before computing the rate.
func computeAgentKPIs(sinceDays int) scorecard.AgentKPIInputs {
	if sinceDays <= 0 {
		sinceDays = 7
	}
	since := time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
	extracted, err := prompts.Extract(prompts.ExtractOptions{Since: since})
	if err != nil || len(extracted) == 0 {
		return scorecard.AgentKPIInputs{}
	}
	filtered := filterAutonomousLoopSentinels(extracted)
	f := prompts.Analyze(filtered)
	if f.TotalPrompts == 0 {
		return scorecard.AgentKPIInputs{}
	}
	cgr := 100.0 * float64(f.Acknowledgements) / float64(f.TotalPrompts)
	rgr := 100.0 * float64(f.Regenerates) / float64(f.TotalPrompts)
	out := scorecard.AgentKPIInputs{
		ConfirmationGateRatePct:  cgr,
		ConfirmationGateComputed: true,
		RegenerateRatePct:        rgr,
		RegenerateComputed:       true,
	}
	if toolEvs, err := tools.Extract(tools.ExtractOptions{Since: since}); err == nil && len(toolEvs) > 0 {
		ts := tools.Analyze(toolEvs)
		if ts.TotalToolCalls > 0 {
			out.ToolSuccessRatePct = ts.SuccessRate
			out.ToolSuccessComputed = true
			out.DestructiveRatePct = ts.DestructiveRate
			out.DestructiveComputed = true
		}
	}
	return out
}

// filterAutonomousLoopSentinels drops `continue` / `proceed` /
// `keep going` prompts that repeat >5x in a single session. Those
// are /loop dynamic-mode pacing sentinels (the harness wakes the
// agent), not real human acks. Counting them in CGR penalizes the
// operator's autonomous workflows. The 5-times threshold is the
// gap between "I clicked continue twice" (real human steering) and
// "the autonomous loop fired 50+ times" (synthetic).
func filterAutonomousLoopSentinels(in []prompts.UserPrompt) []prompts.UserPrompt {
	type key struct {
		session, text string
	}
	counts := map[key]int{}
	sentinel := map[string]bool{
		"continue": true, "proceed": true, "keep going": true,
	}
	for _, p := range in {
		lc := normalizeLoopSentinel(p.Text)
		if sentinel[lc] {
			counts[key{p.SessionID, lc}]++
		}
	}
	out := in[:0]
	for _, p := range in {
		lc := normalizeLoopSentinel(p.Text)
		if sentinel[lc] && counts[key{p.SessionID, lc}] > 5 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func normalizeLoopSentinel(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	return s
}
