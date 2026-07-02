package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/formatter"
)

// newFmtBenchCmd measures the deterministic compression the formatter set
// achieves over a corpus of captured command outputs. Each corpus file is
// named "<command>.<label>.txt" (e.g. "git.status.txt", "go.test.txt"); the
// leading token selects the formatter. The command reports per-file and
// aggregate byte / estimated-token savings at every loss level, so operators
// (and this project's own benchmarks) can quote real numbers rather than
// claims.
func newFmtBenchCmd() *cobra.Command {
	var corpusDir string
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Measure formatter savings over a corpus of captured command outputs",
		Long: `bench runs every file in a corpus directory through the
deterministic formatter set at each loss level and reports byte and
estimated-token savings. Corpus files are named <command>.<label>.txt so the
leading token selects the formatter (git.status.txt -> git formatter).

  tokenops fmt bench --corpus internal/contexts/optimization/formatter/testdata/corpus`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if corpusDir == "" {
				return fmt.Errorf("bench: --corpus is required")
			}
			entries, err := os.ReadDir(corpusDir)
			if err != nil {
				return fmt.Errorf("bench: read corpus: %w", err)
			}
			var files []string
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
					continue
				}
				files = append(files, e.Name())
			}
			sort.Strings(files)
			if len(files) == 0 {
				return fmt.Errorf("bench: no .txt corpus files in %s", corpusDir)
			}

			levels := []formatter.LossLevel{
				formatter.LossConservative,
				formatter.LossBalanced,
				formatter.LossAggressive,
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%-22s %8s  %s\n", "FILE (command)", "RAW B", "SAVINGS (bytes / est-tok / %) per level")
			fmt.Fprintf(out, "%-22s %8s  %-24s %-24s %-24s\n", "", "", "conservative", "balanced", "aggressive")

			var totalRaw int
			totals := map[formatter.LossLevel]int{} // saved bytes per level
			for _, name := range files {
				raw, err := os.ReadFile(filepath.Join(corpusDir, name))
				if err != nil {
					return fmt.Errorf("bench: read %s: %w", name, err)
				}
				command := strings.SplitN(name, ".", 2)[0]
				totalRaw += len(raw)

				cols := make([]string, 0, len(levels))
				for _, lvl := range levels {
					reg := formatter.NewRegistry(formatter.LossPolicy{Default: lvl}, defaultFormatters()...)
					res, _ := reg.Format([]string{command}, raw)
					saved := len(raw) - res.BytesAfter
					if saved < 0 {
						saved = 0
					}
					totals[lvl] += saved
					pct := 0.0
					if len(raw) > 0 {
						pct = 100 * float64(saved) / float64(len(raw))
					}
					flag := ""
					if !res.CriticalKept {
						flag = "!" // critical-line guard tripped -> raw passthrough
					}
					cols = append(cols, fmt.Sprintf("%6d / %5d / %3.0f%%%s", saved, estTokens(saved), pct, flag))
				}
				fmt.Fprintf(out, "%-22s %8d  %-24s %-24s %-24s\n",
					truncName(name, 22), len(raw), cols[0], cols[1], cols[2])
			}

			fmt.Fprintln(out, strings.Repeat("─", 100))
			aggCols := make([]string, 0, len(levels))
			for _, lvl := range levels {
				pct := 0.0
				if totalRaw > 0 {
					pct = 100 * float64(totals[lvl]) / float64(totalRaw)
				}
				aggCols = append(aggCols, fmt.Sprintf("%6d / %5d / %3.0f%%", totals[lvl], estTokens(totals[lvl]), pct))
			}
			fmt.Fprintf(out, "%-22s %8d  %-24s %-24s %-24s\n",
				fmt.Sprintf("TOTAL (%d files)", len(files)), totalRaw, aggCols[0], aggCols[1], aggCols[2])
			return nil
		},
	}
	cmd.Flags().StringVar(&corpusDir, "corpus", "", "directory of <command>.<label>.txt capture files")
	return cmd
}

// truncate shortens s to n runes with an ellipsis.
func truncName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
