package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// newDXCmd reports what agent sessions are like to work with, as opposed
// to what they cost.
//
// The wedge scorecard answers how efficiently tokens were spent. Cost can
// look healthy while the experience is poor — a request that takes
// fourteen turns and two interruptions is a bad session however cheap its
// tokens were — and nothing measured that until now.
func newDXCmd() *cobra.Command {
	var (
		root    string
		days    int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "dx",
		Short: "Agent developer-experience metrics from your session transcripts",
		Long: `dx measures what your agent sessions are like to work with: how many
turns a typical instruction costs, how often the agent redoes its own
work, how often you have to interrupt it.

Every metric is derived from transcripts the client already writes — no
proxy, no extra instrumentation. Work is grouped by operator instruction:
a prompt you typed, and everything the agent did before the next one.

Time-to-first-token is deliberately absent. The field exists on
PromptEvent, but a transcript records when a turn finished, never when it
started streaming, so no passive reader can populate it honestly. It stays
proxy-only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := agentdx.ExtractOptions{Root: root}
			if days > 0 {
				opts.Since = time.Now().AddDate(0, 0, -days)
			}
			records, err := agentdx.Extract(opts)
			if err != nil {
				return err
			}
			m := agentdx.Compute(records)
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(m)
			}
			writeDXText(cmd.OutOrStdout(), m, days)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "transcript root (defaults to ~/.claude/projects)")
	cmd.Flags().IntVar(&days, "days", 7, "window in days; 0 reads everything")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeDXText(w io.Writer, m agentdx.Metrics, days int) {
	window := "all history"
	if days > 0 {
		window = fmt.Sprintf("last %dd", days)
	}
	fmt.Fprintf(w, "Agent DX — %s\n", window)
	fmt.Fprintf(w, "  %d instructions across %d sessions\n\n", m.Prompts, m.Sessions)

	if m.Prompts == 0 {
		fmt.Fprintln(w, "  no instructions in this window — widen with --days 0")
		return
	}

	fmt.Fprintln(w, "EFFORT PER INSTRUCTION")
	fmt.Fprintf(w, "  turns (median):        %.1f\n", m.MedianTurnsPerPrompt)
	fmt.Fprintf(w, "  turns (p90):           %.1f%s\n", m.P90TurnsPerPrompt,
		tailNote(m.MedianTurnsPerPrompt, m.P90TurnsPerPrompt))
	fmt.Fprintf(w, "  context growth/turn:   %d tokens (median)\n\n", m.MedianContextGrowthTokens)

	fmt.Fprintln(w, "FRICTION")
	fmt.Fprintf(w, "  rework rate:           %.1f%%  (edits revisiting a file within one instruction)\n", m.ReworkRatePct)
	fmt.Fprintf(w, "  interrupt rate:        %.1f%%  (instructions you had to stop)\n", m.InterruptRatePct)
	fmt.Fprintf(w, "  escalation rate:       %.1f%%  (instructions delegated to a subagent)\n", m.EscalationRatePct)
	fmt.Fprintf(w, "  compactions/session:   %.1f\n", m.CompactionsPerSession)
}

// tailNote flags a heavy tail, where most instructions are cheap but a
// minority drag — the shape a median alone hides.
func tailNote(median, p90 float64) string {
	if median > 0 && p90 >= median*3 {
		return "  ← heavy tail: a minority of instructions cost far more than typical"
	}
	return ""
}
