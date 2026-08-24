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
		source  string
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
			opts := agentdx.ExtractOptions{
				Root:   root,
				Source: agentdx.Source(source),
			}
			if days > 0 {
				opts.Since = time.Now().AddDate(0, 0, -days)
			}
			records, err := agentdx.ExtractAll(opts)
			if err != nil {
				// A reader that broke is reported, but whatever the other
				// clients yielded is still worth showing.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
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
	cmd.Flags().StringVar(&root, "root", "", "transcript root (defaults per source)")
	cmd.Flags().StringVar(&source, "source", "auto", "client: auto | claude-code | codex | cursor")
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

	g := agentdx.Grade(m)

	fmt.Fprintln(w, "EFFORT PER INSTRUCTION")
	fmt.Fprintf(w, "  turns (median):        %-10.1f %s\n", m.MedianTurnsPerPrompt, badge(g.Turns))
	fmt.Fprintf(w, "  turns (p90):           %-10.1f %s\n", m.P90TurnsPerPrompt,
		tailNote(m.MedianTurnsPerPrompt, m.P90TurnsPerPrompt))
	fmt.Fprintf(w, "  wall-clock (median):   %-10s %s\n", humanSeconds(m.MedianSecondsPerPrompt), badge(g.Duration))
	fmt.Fprintf(w, "  wall-clock (p90):      %s\n", humanSeconds(m.P90SecondsPerPrompt))
	fmt.Fprintf(w, "  tokens (median):       %d\n", m.MedianTokensPerPrompt)
	fmt.Fprintf(w, "  tool calls (median):   %.1f\n", m.MedianToolCallsPerPrompt)
	fmt.Fprintf(w, "  context growth/turn:   %-10d %s\n\n", m.MedianContextGrowthTokens, badge(g.ContextGrowth))

	fmt.Fprintln(w, "FRICTION")
	fmt.Fprintf(w, "  first-try rate:        %-10s %s  (no rework, no interrupt, no delegation)\n",
		fmt.Sprintf("%.1f%%", m.FirstTryRatePct), badge(g.FirstTry))
	fmt.Fprintf(w, "  rework rate:           %-10s %s  (edits revisiting a file within one instruction)\n",
		fmt.Sprintf("%.1f%%", m.ReworkRatePct), badge(g.Rework))
	fmt.Fprintf(w, "  interrupt rate:        %-10s %s  (instructions you had to stop)\n",
		fmt.Sprintf("%.1f%%", m.InterruptRatePct), badge(g.Interrupt))
	fmt.Fprintf(w, "  escalation rate:       %-10s %s  (instructions delegated to a subagent)\n",
		fmt.Sprintf("%.1f%%", m.EscalationRatePct), badge(g.Escalation))
	fmt.Fprintf(w, "  compactions/session:   %-10.1f %s\n", m.CompactionsPerSession, badge(g.Compaction))

	if g.Overall != "" {
		fmt.Fprintf(w, "\nOverall: %s  (the worst grade, not the average — an experience is\n", g.Overall)
		fmt.Fprintln(w, "         only as good as its sharpest friction)")
	}
	if rec, ok := agentdx.Recommend(m); ok {
		fmt.Fprintf(w, "\nBIGGEST WIN\n  %s\n  %s\n  Do: %s\n", rec.Title, rec.Evidence, rec.Action)
	}
}

// badge renders a grade, or nothing for a metric that was not measured.
func badge(l agentdx.Letter) string {
	if l == "" {
		return ""
	}
	return "[" + string(l) + "]"
}

// humanSeconds renders a duration compactly.
func humanSeconds(s float64) string {
	switch {
	case s <= 0:
		return "n/a"
	case s >= 3600:
		return fmt.Sprintf("%.1fh", s/3600)
	case s >= 60:
		return fmt.Sprintf("%.1fm", s/60)
	default:
		return fmt.Sprintf("%.0fs", s)
	}
}

// tailNote flags a heavy tail, where most instructions are cheap but a
// minority drag — the shape a median alone hides.
func tailNote(median, p90 float64) string {
	if median > 0 && p90 >= median*3 {
		return "  ← heavy tail: a minority of instructions cost far more than typical"
	}
	return ""
}
