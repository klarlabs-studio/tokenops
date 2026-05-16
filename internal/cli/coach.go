package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/prompts"
)

// newCoachCmd is the tree for prompt + workflow coaching. For now
// only the `prompts` subcommand is wired — workflow-trace coaching
// lives in `tokenops replay` (which the docs cross-reference).
func newCoachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coach",
		Short: "Analyze your prompting and workflow patterns for waste + anti-patterns",
	}
	cmd.AddCommand(newCoachPromptsCmd())
	return cmd
}

// newCoachPromptsCmd reads Claude Code session JSONLs (the same
// files the claudecodejsonl poller scans) and runs heuristic
// prompt-quality rules. Reads JSONLs directly so prompt text never
// lands in the event store — privacy-respecting and zero schema
// change.
func newCoachPromptsCmd() *cobra.Command {
	var (
		sinceFlag string
		root      string
		session   string
		limit     int
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "prompts",
		Short: "Score your Claude Code prompting against rule-based heuristics",
		Long: `prompts walks ~/.claude/projects/**/*.jsonl, extracts every
human-typed turn (tool results and synthetic system messages are
filtered), and reports:

  - Length distribution (under-5-word / 5-15 / 15-50 / 50-200 / >200)
  - Vague-short prompts (<15 chars, ≤3 words)
  - Pure acknowledgements (yes/no/ok/continue)
  - Short questions (<60 chars with '?')
  - Repeated prompts (same text issued 3+ times)
  - Concrete recommendations

Prompt text is read from the JSONLs at scan time — never persisted to
the TokenOps event store. --json emits machine-readable findings for
agents to consume.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := prompts.ExtractOptions{
				Root:      root,
				SessionID: session,
				Limit:     limit,
			}
			if sinceFlag != "" {
				since, err := parseSince(sinceFlag)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				opts.Since = since
			}
			extracted, err := prompts.Extract(opts)
			if err != nil {
				return fmt.Errorf("extract: %w", err)
			}
			findings := prompts.Analyze(extracted)
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(findings)
			}
			renderCoachPrompts(cmd, findings, opts.Since)
			return nil
		},
	}
	cmd.Flags().StringVar(&sinceFlag, "since", "7d", "lower bound: RFC3339 timestamp or duration like 24h or 7d")
	cmd.Flags().StringVar(&root, "root", "", "JSONL scan root (defaults to ~/.claude/projects)")
	cmd.Flags().StringVar(&session, "session", "", "restrict to a single session id (filename stem)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max prompts to extract (0 = unbounded)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	return cmd
}

func renderCoachPrompts(cmd *cobra.Command, f prompts.Findings, since time.Time) {
	out := cmd.OutOrStdout()
	header := "Prompting coach"
	if !since.IsZero() {
		header = fmt.Sprintf("Prompting coach — since %s", since.Format(time.RFC3339))
	}
	fmt.Fprintln(out, header)
	fmt.Fprintf(out, "  total prompts: %d  |  avg %.0f chars / %.0f words  |  min %d, max %d chars\n",
		f.TotalPrompts, f.AvgChars, f.AvgWords, f.MinChars, f.MaxChars)
	if f.TotalPrompts == 0 {
		fmt.Fprintln(out, "  (no human prompts in the scan window)")
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "LENGTH DISTRIBUTION")
	keys := []string{"<5w", "5-15w", "15-50w", "50-200w", ">200w"}
	for _, k := range keys {
		n := f.LengthDistribution[k]
		pct := 100 * float64(n) / float64(f.TotalPrompts)
		bar := strings.Repeat("█", int(pct/2))
		fmt.Fprintf(out, "  %-8s %5d  (%5.1f%%) %s\n", k, n, pct, bar)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "FINDINGS")
	fmt.Fprintf(out, "  vague/short (<15 chars, ≤3 words):  %d (%.1f%%)\n",
		f.VagueShort, pctOf(f.VagueShort, f.TotalPrompts))
	for _, s := range f.VagueShortSamples {
		fmt.Fprintf(out, "      • %q\n", s)
	}
	fmt.Fprintf(out, "  pure acknowledgements:              %d (%.1f%%)\n",
		f.Acknowledgements, pctOf(f.Acknowledgements, f.TotalPrompts))
	fmt.Fprintf(out, "  short questions (<60 chars + '?'):  %d (%.1f%%)\n",
		f.ShortQuestions, pctOf(f.ShortQuestions, f.TotalPrompts))
	fmt.Fprintf(out, "  single-sentence no-context:         %d (%.1f%%)\n",
		f.NoContextSingles, pctOf(f.NoContextSingles, f.TotalPrompts))
	if len(f.RepeatedPrompts) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "REPEATED PROMPTS (≥3x)")
		sort.Slice(f.RepeatedPrompts, func(i, j int) bool {
			return f.RepeatedPrompts[i].Count > f.RepeatedPrompts[j].Count
		})
		for _, r := range f.RepeatedPrompts {
			t := r.Text
			if len(t) > 60 {
				t = t[:60] + "…"
			}
			fmt.Fprintf(out, "  %3dx  %q\n", r.Count, t)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "RECOMMENDATIONS")
	for i, r := range f.Recommendations {
		fmt.Fprintf(out, "  %d. %s\n", i+1, r)
	}
}

func pctOf(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(n) / float64(total)
}
