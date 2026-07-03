package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/fmtlearn"
	"github.com/felixgeelhaar/tokenops/internal/infra/jsonlfmt"
	"github.com/felixgeelhaar/tokenops/internal/infra/svgchart"
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
		svgDir   string
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
			if svgDir != "" {
				paths, err := writeAnalyzeSVGs(svgDir, rep)
				if err != nil {
					return err
				}
				for _, p := range paths {
					fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", p)
				}
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
	cmd.Flags().StringVar(&svgDir, "svg", "", "also write composition.svg + reads.svg charts to this directory")
	return cmd
}

// writeAnalyzeSVGs renders the composition + read charts from the report as
// self-contained SVG files and returns their paths. Text uses currentColor so
// an inlined chart themes with the embedding page.
func writeAnalyzeSVGs(dir string, rep *jsonlfmt.Report) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	comp := rep.Composition
	grand := comp.AssistantProse + comp.UserProse
	for _, v := range comp.ByTool {
		grand += v
	}
	if grand == 0 {
		return nil, fmt.Errorf("analyze --svg: no content to chart")
	}
	read := comp.ByTool["Read"]
	bash := comp.ByTool["Bash"]
	rest := grand - read - bash - comp.AssistantProse - comp.UserProse
	frac := func(v int64) float64 { return float64(v) / float64(grand) }
	pctOf := func(v, whole int64) string {
		if whole == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(v)/float64(whole))
	}

	compositionBars := []svgchart.Bar{
		{Label: "File reads", Display: pctOf(read, grand), Frac: frac(read), Note: "source files the agent reads", Highlight: true},
		{Label: "Command output", Display: pctOf(bash, grand), Frac: frac(bash), Note: "git, tests, builds, greps"},
		{Label: "Model prose", Display: pctOf(comp.AssistantProse, grand), Frac: frac(comp.AssistantProse), Note: `what "terse-speak" compresses`},
		{Label: "Everything else", Display: pctOf(rest, grand), Frac: frac(rest), Note: "edits, subagents, screenshots, web"},
		{Label: "Our prompts", Display: pctOf(comp.UserProse, grand), Frac: frac(comp.UserProse)},
	}
	composition := svgchart.HBars("Where the context tokens actually go", compositionBars, svgchart.Options{
		Caption: fmt.Sprintf("tokenops fmt analyze · %d sessions", rep.SessionsScanned),
	})

	// Read chart: first reads vs re-reads, as a share of Read volume.
	rr := rep.Reads
	first := max(rr.RawBytes-rr.RepeatReadBytes, 0)
	readBars := []svgchart.Bar{
		{Label: "First reads", Display: pctOf(first, rr.RawBytes), Frac: fracOf(first, rr.RawBytes), Note: "content the agent had not seen"},
		{Label: "Re-reads (same file again)", Display: pctOf(rr.RepeatReadBytes, rr.RawBytes), Frac: fracOf(rr.RepeatReadBytes, rr.RawBytes), Note: "mostly ranged / intentional — little is reclaimable", Highlight: true},
	}
	reads := svgchart.HBars("File reads: how much is a repeat", readBars, svgchart.Options{
		Caption: fmt.Sprintf("tokenops fmt analyze · %s ranged", pctOf(int64(rr.RangedReads), int64(rr.Reads))),
	})

	// fmt ROI on real command output: what the formatters actually save on
	// this user's Bash volume (as opposed to a benchmark corpus).
	roiBars := []svgchart.Bar{
		{Label: "Balanced", Display: pctOf(rep.SavedBalanced, rep.TotalBashBytes), Frac: fracOf(rep.SavedBalanced, rep.TotalBashBytes), Note: "conservative loss level"},
		{Label: "Aggressive", Display: pctOf(rep.SavedAggressive, rep.TotalBashBytes), Frac: fracOf(rep.SavedAggressive, rep.TotalBashBytes), Note: "maximum loss level", Highlight: true},
	}
	roi := svgchart.HBars("What the formatters save on our command output", roiBars, svgchart.Options{
		Caption: "tokenops fmt analyze · vs 57–68% on the benchmark corpus",
	})

	out := []string{
		filepath.Join(dir, "composition.svg"),
		filepath.Join(dir, "reads.svg"),
		filepath.Join(dir, "fmt-roi.svg"),
	}
	svgs := []string{composition, reads, roi}
	for i, p := range out {
		if err := os.WriteFile(p, []byte(svgs[i]), 0o644); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func fracOf(v, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(v) / float64(whole)
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
	// Read side — usually the biggest lever, and one fmt does NOT address.
	rr := rep.Reads
	if rr.Reads > 0 {
		fmt.Fprintf(out, "\nRead (file content — %s tokens, %d reads, the biggest context slice):\n",
			humanTokens(jsonlfmt.EstTokens(rr.RawBytes)), rr.Reads)
		fmt.Fprintf(out, "  already ranged (offset/limit): %.0f%% of reads\n", pct(int64(rr.RangedReads), int64(rr.Reads)))
		fmt.Fprintf(out, "  re-reads (same file re-read in a session): ~%s tokens (%.0f%% of Read) — avoidable\n",
			humanTokens(jsonlfmt.EstTokens(rr.RepeatReadBytes)), pct(rr.RepeatReadBytes, rr.RawBytes))
		fmt.Fprintf(out, "  duplicate content (byte-identical re-sent):  ~%s tokens (%.0f%% of Read)\n",
			humanTokens(jsonlfmt.EstTokens(rr.DupContentBytes)), pct(rr.DupContentBytes, rr.RawBytes))
		if len(rr.TopReReads) > 0 {
			fmt.Fprintln(out, "  most re-read files (wasted tokens):")
			for i, f := range rr.TopReReads {
				if i >= 6 {
					break
				}
				fmt.Fprintf(out, "    %-52s %sx  ~%s\n",
					truncName(f.Path, 52), fmtInt(f.Reads), humanTokens(jsonlfmt.EstTokens(f.WastedBytes)))
			}
		}
		fmt.Fprintln(out, "  note: re-reads/dupes are a context-management issue, not a formatter one —")
		fmt.Fprintln(out, "  addressable by the proxy dedupe/context-trim optimizers or by re-reading less.")
	}

	fmt.Fprintln(out, "\nRun `tokenops fmt hook` + `export TOKENOPS_FMT=1` to capture the Bash savings live.")
}

// fmtInt renders an int without thousands separators (small counts).
func fmtInt(n int) string { return fmt.Sprintf("%d", n) }

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
