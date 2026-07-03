package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/fmtlearn"
	"github.com/felixgeelhaar/tokenops/internal/infra/jsonlfmt"
)

// newFmtAnalyzeCmd mines the Claude Code JSONL logs directly — no daemon, no
// wrapped commands, no setup — to show what actually fills your context and
// what `tokenops fmt` would save on your real traffic. This is the
// self-wiring entry point: point it at logs that already exist and it
// answers "where are my tokens going and what would fmt do about it".
func newFmtAnalyzeCmd(rf *rootFlags) *cobra.Command {
	var (
		root     string
		jsonOut  bool
		top      int
		maxFiles int
	)
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Mine Claude Code logs for context composition + fmt ROI (no setup, no daemon)",
		Long: `analyze reads your Claude Code JSONL logs (~/.claude/projects),
measures what fills your context (Read vs Bash vs prose), and dry-runs every
Bash command's output through the formatter engine to estimate what
tokenops fmt would save on your real traffic. Nothing is persisted — only
sizes are reported. Requires no daemon and no wrapped commands.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, _, err := jsonlfmt.Scan(registryFormatters(rf), jsonlfmt.Options{
				Root: root, MaxFiles: maxFiles,
			}, time.Now())
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			renderAnalyze(cmd, rep, top)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Claude Code projects dir (defaults to ~/.claude/projects)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the report as JSON")
	cmd.Flags().IntVar(&top, "top", 15, "show the top N Bash commands by output volume")
	cmd.Flags().IntVar(&maxFiles, "max-files", 0, "cap sessions scanned (newest first); 0 = all")
	return cmd
}

func renderAnalyze(cmd *cobra.Command, rep *jsonlfmt.Report, top int) {
	out := cmd.OutOrStdout()
	if rep.SessionsScanned == 0 {
		fmt.Fprintf(out, "No Claude Code logs found under %s.\n", rep.Root)
		return
	}

	// Composition: tool_result by tool + prose, by byte volume.
	type row struct {
		name  string
		bytes int64
	}
	var rows []row
	var toolTotal int64
	for name, b := range rep.Composition.ByTool {
		rows = append(rows, row{name, b})
		toolTotal += b
	}
	rows = append(rows,
		row{"(assistant prose)", rep.Composition.AssistantProse},
		row{"(user prose)", rep.Composition.UserProse},
	)
	grand := toolTotal + rep.Composition.AssistantProse + rep.Composition.UserProse
	sort.Slice(rows, func(i, j int) bool { return rows[i].bytes > rows[j].bytes })

	fmt.Fprintf(out, "Context composition — %d sessions, %d tool results (%s)\n\n",
		rep.SessionsScanned, rep.ToolResults, rep.Root)
	fmt.Fprintf(out, "  %-20s %14s %8s\n", "SOURCE", "~TOKENS", "SHARE")
	for _, r := range rows {
		if r.bytes == 0 {
			continue
		}
		fmt.Fprintf(out, "  %-20s %14s %7.1f%%\n",
			r.name, humanTokens(jsonlfmt.EstTokens(r.bytes)), pct(r.bytes, grand))
	}

	// fmt ROI on the Bash slice.
	fmt.Fprintf(out, "\nfmt would compress the Bash output (%s tokens across %d results):\n",
		humanTokens(jsonlfmt.EstTokens(rep.TotalBashBytes)), bashRuns(rep))
	fmt.Fprintf(out, "  balanced:   ~%s tokens saved (%.0f%% of Bash)\n",
		humanTokens(jsonlfmt.EstTokens(rep.SavedBalanced)), pct(rep.SavedBalanced, rep.TotalBashBytes))
	fmt.Fprintf(out, "  aggressive: ~%s tokens saved (%.0f%% of Bash)\n",
		humanTokens(jsonlfmt.EstTokens(rep.SavedAggressive)), pct(rep.SavedAggressive, rep.TotalBashBytes))

	// Top commands by raw volume, with per-command savings + coverage.
	fmt.Fprintf(out, "\nTop commands by output volume:\n")
	fmt.Fprintf(out, "  %-14s %6s %12s %10s %10s %s\n", "COMMAND", "RUNS", "~RAW TOK", "BAL %", "AGG %", "FORMATTER")
	shown := 0
	for _, c := range rep.Commands {
		if shown >= top {
			break
		}
		cover := "generic (candidate)"
		if c.Handled {
			cover = "dedicated"
		}
		fmt.Fprintf(out, "  %-14s %6d %12s %9.0f%% %9.0f%% %s\n",
			c.Command, c.Runs, humanTokens(jsonlfmt.EstTokens(c.RawBytes)),
			pct(c.SavedBalanced, c.RawBytes), pct(c.SavedAggressive, c.RawBytes), cover)
		shown++
	}
	fmt.Fprintln(out, "\nRun `tokenops fmt hook` + `export TOKENOPS_FMT=1` to capture these savings live.")
}

// jsonlLearnRecords returns fmtlearn records synthesised from the JSONL so
// `fmt learn` reflects real usage without any wrapped runs. Best-effort:
// scan errors yield an empty slice. Capped for responsiveness.
func jsonlLearnRecords(rf *rootFlags, maxFiles int) []fmtlearn.Record {
	_, recs, err := jsonlfmt.Scan(registryFormatters(rf), jsonlfmt.Options{MaxFiles: maxFiles}, time.Now())
	if err != nil {
		return nil
	}
	return recs
}

func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

func bashRuns(rep *jsonlfmt.Report) int {
	n := 0
	for _, c := range rep.Commands {
		n += c.Runs
	}
	return n
}

// humanTokens renders a token count as e.g. "8.1M", "412k", "980".
func humanTokens(t int64) string {
	switch {
	case t >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(t)/1e6)
	case t >= 1_000:
		return fmt.Sprintf("%.0fk", float64(t)/1e3)
	default:
		return fmt.Sprintf("%d", t)
	}
}
