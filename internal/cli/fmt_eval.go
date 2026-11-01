package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/fmtinsight"
)

// newFmtEvalCmd reports the fmt evaluation: what fmt never drops, then how
// much of the evidence an agent needs it keeps, then what it saves.
func newFmtEvalCmd() *cobra.Command {
	var (
		corpusDir string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Show what fmt never drops, how much evidence it keeps, and what it saves",
		Long: `eval runs the built-in formatters over a corpus of command outputs whose
evidence is labeled by hand — failing tests, file:line of errors, error
messages, changed paths, exit status, failed resources, counts — and reports
the share of that evidence still recoverable from the compact output at each
loss level, next to the bytes and estimated tokens saved.

It leads with the invariant: every line a formatter marks critical survives
verbatim at every level, or the raw output is forwarded instead. The labels
are independent of the formatters, so the corpus also catches a line an
agent needs that a formatter never called critical; any critical item lost
fails the command.

With no flag it evaluates the corpus bundled with TokenOps. --corpus reads
your own: <id>.txt outputs, each with an <id>.json label listing its
evidence; the id's first token picks the formatter (go.fail.txt -> go).

Recall says what the agent can still find, not whether the work went
better; 'tokenops fmt experiment' compares sessions on outcomes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, err := fmtinsight.Recall(corpusDir)
			if err != nil {
				return fmt.Errorf("eval: %w", err)
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			} else {
				renderFmtEval(out, rep)
			}
			if n := len(rep.CriticalMisses); n > 0 {
				return fmt.Errorf("eval: %d critical evidence item(s) lost", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&corpusDir, "corpus", "", "directory of labeled outputs (<id>.txt with <id>.json); default: the bundled corpus")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the report as JSON")
	return cmd
}

// renderFmtEval prints the invariant, then recall and savings per
// formatter, then what the lossier levels give up.
func renderFmtEval(w io.Writer, rep fmtinsight.RecallReport) {
	if len(rep.Levels) == 0 {
		return
	}
	first := rep.Levels[0].Overall
	critTotal, critKept := 0, 0
	for _, lv := range rep.Levels {
		critTotal += lv.Overall.CriticalItems
		critKept += lv.Overall.CriticalRecalled
	}

	fmt.Fprintln(w, "What fmt never drops")
	for _, line := range wrapWords(rep.Invariant, 76) {
		fmt.Fprintln(w, "  "+line)
	}
	if critKept == critTotal {
		fmt.Fprintf(w, "  Labeled corpus: %d/%d critical items recovered at every level (%d outputs, %d formatters).\n",
			first.CriticalItems, first.CriticalItems, first.Cases, rep.Formatters)
	} else {
		fmt.Fprintf(w, "  Labeled corpus: %d of %d critical items lost across levels:\n", critTotal-critKept, critTotal)
		for _, m := range rep.CriticalMisses {
			fmt.Fprintf(w, "    %-18s %-14s %-16s %s\n", m.Case, m.Formatter, m.Kind, m.Text)
		}
	}

	fmt.Fprintf(w, "\nEvidence recall and savings (%s corpus)\n", rep.Corpus)
	fmt.Fprintf(w, "%-16s %5s %6s   %-22s   %s\n", "FORMATTER", "CASES", "ITEMS", "RECALL cons/bal/aggr", "SAVED cons/bal/aggr")
	byLevel := make([]map[string]fmtinsight.FormatterRecall, len(rep.Levels))
	for i, lv := range rep.Levels {
		byLevel[i] = map[string]fmtinsight.FormatterRecall{}
		for _, fr := range lv.Formatters {
			byLevel[i][fr.Formatter] = fr
		}
	}
	for _, fr := range rep.Levels[0].Formatters {
		recall, saved := make([]string, 0, 3), make([]string, 0, 3)
		for i := range rep.Levels {
			t := byLevel[i][fr.Formatter]
			recall = append(recall, fmt.Sprintf("%3.0f%%", 100*t.Recall()))
			saved = append(saved, fmt.Sprintf("%3.0f%%", t.SavedPct()))
		}
		fmt.Fprintf(w, "%-16s %5d %6d   %-22s   %s\n", fr.Formatter, fr.Cases, fr.Items,
			strings.Join(recall, " / "), strings.Join(saved, " / "))
	}
	recall, saved, tokens := make([]string, 0, 3), make([]string, 0, 3), make([]string, 0, 3)
	for _, lv := range rep.Levels {
		recall = append(recall, fmt.Sprintf("%3.0f%%", 100*lv.Overall.Recall()))
		saved = append(saved, fmt.Sprintf("%3.0f%%", lv.Overall.SavedPct()))
		tokens = append(tokens, fmt.Sprintf("%d", lv.Overall.SavedTokens()))
	}
	fmt.Fprintln(w, strings.Repeat("─", 84))
	fmt.Fprintf(w, "%-16s %5d %6d   %-22s   %s\n", "TOTAL", first.Cases, first.Items,
		strings.Join(recall, " / "), strings.Join(saved, " / "))
	fmt.Fprintf(w, "%-16s %5s %6s   %-22s   %s est. tokens saved\n", "", "", "", "", strings.Join(tokens, " / "))

	var gaveUp []string
	for _, lv := range rep.Levels {
		for _, m := range lv.Misses {
			if !m.Critical {
				gaveUp = append(gaveUp, fmt.Sprintf("  %-12s %-18s %-14s %s", lv.Level, m.Case, m.Kind, m.Text))
			}
		}
	}
	if len(gaveUp) > 0 {
		fmt.Fprintln(w, "\nWhat the lossier levels give up (non-critical items)")
		for _, l := range gaveUp {
			fmt.Fprintln(w, l)
		}
	}

	fmt.Fprintln(w, "\nRecall says what the agent can still find, not whether the work went better:")
	fmt.Fprintln(w, "'tokenops fmt experiment' compares sessions with and without fmt on outcomes.")
}

// wrapWords breaks s into lines of at most width runes at word boundaries.
func wrapWords(s string, width int) []string {
	var lines []string
	var cur strings.Builder
	for _, word := range strings.Fields(s) {
		if cur.Len() > 0 && cur.Len()+1+len(word) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(word)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
