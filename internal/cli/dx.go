package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
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
		fresh   bool

		includeScratch bool
	)
	cmd := &cobra.Command{
		Use:   "dx",
		Short: "How your sessions go: turns, rework, interrupts, and the one change to make",
		Long: `dx measures what your agent sessions are like to work with: how many
turns a typical instruction costs, how often the agent redoes its own
work, how often you have to interrupt it.

Every metric is derived from transcripts the client already writes — no
proxy, no extra instrumentation. Work is grouped by operator instruction:
a prompt you typed, and everything the agent did before the next one.

Sessions run in throwaway directories — a benchmark harness, a temporary
clone, anything under /tmp — are excluded. These metrics claim to describe
how you work, and a simulated session has no operator. Pass
--include-scratch to measure them anyway.

Time-to-first-token is deliberately absent. The field exists on
PromptEvent, but a transcript records when a turn finished, never when it
started streaming, so no passive reader can populate it honestly. It stays
proxy-only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The default week is what the daemon analyses in the
			// background; reading every transcript again takes minutes.
			if !fresh && root == "" && source == "auto" && days == 7 && !includeScratch {
				if done, err := dxFromSnapshot(cmd, jsonOut, days); done || err != nil {
					return err
				}
			}
			dx, curve := sessions.ComputeDXWithCurve(transcriptWindow(root, source, days, includeScratch), time.Now())
			warnRead(cmd, dx.Warnings)
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(dx.Metrics)
			}
			writeDXText(cmd.OutOrStdout(), dx, curve, days)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "transcript root (defaults per source)")
	cmd.Flags().StringVar(&source, "source", "auto", "client: auto | claude-code | codex | cursor | opencode")
	cmd.Flags().IntVar(&days, "days", 7, "window in days; 0 reads everything")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "read the transcripts now instead of the daemon's recent analysis (takes minutes on a busy machine)")
	cmd.Flags().BoolVar(&includeScratch, "include-scratch", false, scratchFlagHelp)
	return cmd
}

// dxFromSnapshot answers from the daemon's last analysis when it is
// recent; done is false when there is none to answer from.
func dxFromSnapshot(cmd *cobra.Command, jsonOut bool, days int) (bool, error) {
	snap, err := findings.ReadSnapshot(findings.DefaultDir())
	age := time.Since(snapshotTime(snap))
	if err != nil || snap == nil || age > findings.MaxSnapshotAge || snap.DX.Metrics.Prompts == 0 {
		return false, nil
	}
	for _, w := range snap.DX.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return true, enc.Encode(snap.DX.Metrics)
	}
	writeDXText(cmd.OutOrStdout(), snap.DX, snap.Curve, days)
	fmt.Fprintf(cmd.OutOrStdout(), "\nFrom the daemon's analysis %s ago; --fresh reads the transcripts now.\n", agoWords(age))
	return true, nil
}

func snapshotTime(s *findings.SessionsSnapshot) time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.ComputedAt
}

// transcriptWindow is the transcripts a dx, story or verify run reads:
// the last days days, or everything when days is zero or less.
func transcriptWindow(root, source string, days int, includeScratch bool) sessions.Window {
	w := sessions.Window{Root: root, Days: days, Source: source, IncludeScratch: includeScratch}
	if days <= 0 {
		w.All = true
	}
	return w
}

// warnRead reports the clients whose transcripts could not be read. A
// reader that broke is reported, but whatever the others yielded is
// still worth showing.
func warnRead(cmd *cobra.Command, warnings []string) {
	if len(warnings) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", strings.Join(warnings, "\n"))
	}
}

func writeDXText(w io.Writer, dx sessions.DX, bands []sessions.ContextBand, days int) {
	m := dx.Metrics
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

	g := dx.Grades

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
	fmt.Fprintf(w, "  rework rate:           %-10s %s  (edits returning to a file after moving on)\n",
		pctOrNA(m.ReworkRatePct, m.TotalEdits > 0), badge(g.Rework))
	fmt.Fprintf(w, "  interrupt rate:        %-10s %s  (instructions you had to stop)\n",
		fmt.Sprintf("%.1f%%", m.InterruptRatePct), badge(g.Interrupt))
	fmt.Fprintf(w, "  escalation rate:       %-10s %s  (instructions delegated to a subagent)\n",
		fmt.Sprintf("%.1f%%", m.EscalationRatePct), badge(g.Escalation))
	fmt.Fprintf(w, "  compactions/session:   %-10.1f %s\n", m.CompactionsPerSession, badge(g.Compaction))

	if g.Overall != "" {
		overall := string(g.Overall)
		if g.OverallDriver != "" {
			overall += " (" + g.OverallDriver + ")"
		}
		fmt.Fprintf(w, "\nOverall: %s  (the worst grade, not the average — an experience is\n", overall)
		fmt.Fprintln(w, "         only as good as its sharpest friction)")
	}
	if len(m.ByProvider) > 0 {
		fmt.Fprintln(w, "\nBY PROVIDER  (only clients that route to several record this)")
		names := make([]string, 0, len(m.ByProvider))
		for name := range m.ByProvider {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "  %-18s %8s %8s %10s %10s\n",
			"PROVIDER", "PROMPTS", "TURNS", "FIRST-TRY", "REWORK")
		for _, name := range names {
			p := m.ByProvider[name]
			fmt.Fprintf(w, "  %-18s %8d %8.1f %9.1f%% %10s\n",
				name, p.Prompts, p.MedianTurnsPerPrompt, p.FirstTryRatePct,
				pctOrNA(p.ReworkRatePct, p.TotalEdits > 0))
		}
	}

	writeDXEffort(w, m.ByEffort)

	if len(bands) > 0 {
		fmt.Fprintln(w, "\nQUALITY vs CONTEXT")
		fmt.Fprintf(w, "  %-10s %8s %10s %10s %8s\n",
			"CONTEXT", "PROMPTS", "REJECTED", "REPEATED", "TURNS")
		for _, b := range bands {
			fmt.Fprintf(w, "  %-10s %8d %9.1f%% %9.1f%% %8.1f\n",
				b.Label, b.Prompts, b.RejectRatePct, b.RepeatCallRatePct, b.MedianTurns)
		}
		if note := sessions.DegradationNote(bands); note != "" {
			fmt.Fprintf(w, "\n  %s\n", note)
		}
		fmt.Fprintln(w, "\n  REPEATED is the agent re-issuing a call it already made, within its last")
		fmt.Fprintln(w, "  50 — losing track of what it has done. Counted over a fixed lookback so a")
		fmt.Fprintln(w, "  long session cannot inflate it. REJECTED is you saying the reply was wrong.")
		fmt.Fprintln(w, "  Turns are shown for shape only: they fall as context grows because a")
		fmt.Fprintln(w, "  low-context instruction is usually an early one, mid-orientation.")
	}

	if rec := dx.Recommendation; rec != nil {
		fmt.Fprintf(w, "\nBIGGEST WIN\n  %s\n  %s\n  Do: %s\n", rec.Title, rec.Evidence, rec.Action)
	}
	fmt.Fprintln(w, "\nWhat a figure means: tokenops explain <name>, e.g. tokenops explain wall-clock")
}

// writeDXEffort prints the model × effort rows, where clients record
// effort.
func writeDXEffort(w io.Writer, rows []sessions.EffortRow) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w, "\nBY MODEL AND EFFORT")
	fmt.Fprintf(w, "  %-26s %-7s %6s %6s %8s %9s %10s %9s\n",
		"MODEL", "EFFORT", "INSTR", "TURNS", "TIME", "PEAK CTX", "FIRST-TRY", "REJECTED")
	thin := false
	for _, r := range rows {
		mark := ""
		if !r.Enough {
			mark, thin = " *", true
		}
		model := r.Model
		if model == "" {
			model = "(unrecorded)"
		}
		fmt.Fprintf(w, "  %-26s %-7s %6d %6.1f %8s %8dk %9.1f%% %8.1f%%%s\n",
			model, r.Effort, r.Instructions, r.MedianTurns, humanSeconds(r.MedianSeconds),
			r.MedianPeakContext/1000, r.FirstTryPct, r.RejectedPct, mark)
	}
	if thin {
		fmt.Fprintf(w, "  * fewer than %d instructions; too few to compare.\n", sessions.MinEffortInstructions)
	}
	fmt.Fprintln(w, "  Compare levels within one model. You raise effort for harder work, so a")
	fmt.Fprintln(w, "  higher level doing worse may mean harder instructions, not a worse setting.")
}

// pctOrNA renders a percentage, or n/a when nothing was observed behind
// it. A rate with an empty denominator is not zero — showing 0.0% for
// "no edits happened" reads as "the agent never redid its work", which is
// a claim the data does not make.
func pctOrNA(v float64, measured bool) string {
	if !measured {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", v)
}

// badge renders a grade, or nothing for a metric that was not measured.
func badge(l sessions.Letter) string {
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

// agoWords is "2h 39m", "12m" or "under a minute".
func agoWords(d time.Duration) string {
	d = d.Round(time.Minute)
	switch h, m := int(d/time.Hour), int(d%time.Hour/time.Minute); {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	}
	return "under a minute"
}
