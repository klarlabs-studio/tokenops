package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/rules"
	"github.com/felixgeelhaar/tokenops/internal/infra/rulesfs"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Rule Intelligence: analyze, detect conflicts, compress, inject, benchmark",
		Long: `rules analyzes operational rule artifacts (CLAUDE.md, AGENTS.md,
Cursor rules, MCP policies, repo conventions) as first-class telemetry.

This is the entry point for TokenOps Rule Intelligence (issue #12). The
analyze subcommand surfaces per-document and per-section token cost
breakdowns. Future subcommands cover conflict detection, corpus
compression, dynamic injection, and rule benchmarking.`,
	}
	cmd.AddCommand(
		newRulesAnalyzeCmd(),
		newRulesConflictsCmd(),
		newRulesCompressCmd(),
		newRulesInjectCmd(),
		newRulesBenchCmd(),
	)
	return cmd
}

func newRulesBenchCmd() *cobra.Command {
	var (
		specPath string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Benchmark rule profiles across scenarios",
		Long: `bench reads a YAML/JSON spec listing rule profiles and workload
scenarios, runs the router for each profile-scenario pair, and reports a
scoreboard sorted by ROI per scenario. Useful for choosing between rule
systems (e.g. lean vs bloat, refactor vs PR-review).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if specPath == "" {
				return fmt.Errorf("--spec is required")
			}
			data, err := os.ReadFile(specPath)
			if err != nil {
				return fmt.Errorf("read spec: %w", err)
			}
			spec, err := rules.ParseBenchSpec(data)
			if err != nil {
				return err
			}
			res, err := rules.RunBenchSpec(spec, rulesfs.LoadCorpus)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			renderBenchText(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&specPath, "spec", "", "path to bench spec (YAML or JSON)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	return cmd
}

func renderBenchText(cmd *cobra.Command, res *rules.BenchmarkResult) {
	out := cmd.OutOrStdout()
	if len(res.Scores) == 0 {
		fmt.Fprintln(out, "no scores")
		return
	}
	fmt.Fprintf(out, "%-24s %-16s %5s %10s %10s %8s\n",
		"SCENARIO", "PROFILE", "SECT", "CTX_TOK", "SAVED", "ROI")
	for _, s := range res.Scores {
		fmt.Fprintf(out, "%-24s %-16s %5d %10d %10d %8.2f\n",
			truncateRule(s.Scenario, 24), truncateRule(s.Profile, 16),
			s.Sections, s.ContextTokens, s.TokensSaved, s.ROIScore)
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "winners:")
	for sc, p := range res.Winners {
		fmt.Fprintf(out, "  %s -> %s\n", sc, p)
	}
}

func newRulesInjectCmd() *cobra.Command {
	var (
		root      string
		repoID    string
		workflow  string
		agent     string
		files     []string
		tools     []string
		keywords  []string
		budget    int64
		minScore  float64
		latencyMS int64
		global    bool
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "inject",
		Short: "Preview the dynamic rule subset selected for a request context",
		Long: `inject runs the rule router against the corpus at --root using the
supplied signals (--workflow, --agent, --file, --tool, --keyword) and
prints the chosen sections plus the rationale for each. Use this to dry-
run the dynamic injection policy before wiring it into the proxy.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
				root = wd
			}
			docs, err := rulesfs.LoadCorpus(root, repoID)
			if err != nil {
				return err
			}
			cfg := rules.RouterConfig{
				TokenBudget:        budget,
				MinScore:           minScore,
				IncludeGlobalScope: global,
			}
			if latencyMS > 0 {
				cfg.LatencyBudget = time.Duration(latencyMS) * time.Millisecond
			}
			r := rules.NewRouter(cfg)
			res := r.Select(docs, rules.SelectionSignals{
				WorkflowID: workflow,
				AgentID:    agent,
				RepoID:     repoID,
				FilePaths:  files,
				Tools:      tools,
				Keywords:   keywords,
			})
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			renderInjectText(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "repository root to scan")
	cmd.Flags().StringVar(&repoID, "repo-id", "", "opaque repository identifier")
	cmd.Flags().StringVar(&workflow, "workflow", "", "active workflow identifier")
	cmd.Flags().StringVar(&agent, "agent", "", "active agent identifier")
	cmd.Flags().StringSliceVar(&files, "file", nil, "file path the request touches (repeat for multiple)")
	cmd.Flags().StringSliceVar(&tools, "tool", nil, "tool invoked by the request (repeat for multiple)")
	cmd.Flags().StringSliceVar(&keywords, "keyword", nil, "keyword extracted from the prompt (repeat for multiple)")
	cmd.Flags().Int64Var(&budget, "token-budget", 0, "maximum total token cost of the selected subset (0 = unbounded)")
	cmd.Flags().Float64Var(&minScore, "min-score", 1.0, "minimum relevance score a section must reach to be admitted")
	cmd.Flags().Int64Var(&latencyMS, "latency-budget-ms", 0, "wall-clock latency budget in ms (0 = unbounded)")
	cmd.Flags().BoolVar(&global, "include-global", true, "always admit globally-scoped rules")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	return cmd
}

func renderInjectText(cmd *cobra.Command, res *rules.SelectionResult) {
	out := cmd.OutOrStdout()
	if len(res.Selections) == 0 {
		fmt.Fprintln(out, "no sections selected")
	} else {
		fmt.Fprintf(out, "%-56s %8s %6s\n", "SECTION", "TOKENS", "SCORE")
		for _, s := range res.Selections {
			fmt.Fprintf(out, "%-56s %8d %6.2f\n",
				truncateRule(s.SectionID, 56), s.TokenCount, s.Score)
			if len(s.Reasons) > 0 {
				fmt.Fprintf(out, "    reasons: %s\n", strings.Join(s.Reasons, ", "))
			}
		}
	}
	fmt.Fprintf(out, "\nconsidered=%d selected=%d skipped=%d tokens=%d budget_hit=%v truncated=%v elapsed_ns=%d\n",
		res.Considered, len(res.Selections), res.SkippedCount, res.TotalTokens, res.BudgetHit, res.Truncated, res.ElapsedNS)
}

type compressView struct {
	SourceID         string                    `json:"source_id"`
	Path             string                    `json:"path"`
	OriginalTokens   int64                     `json:"original_tokens"`
	CompressedTokens int64                     `json:"compressed_tokens"`
	QualityScore     float64                   `json:"quality_score"`
	Accepted         bool                      `json:"accepted"`
	Sections         []rules.CompressedSection `json:"sections,omitempty"`
	Body             string                    `json:"body,omitempty"`
}

func newRulesCompressCmd() *cobra.Command {
	var (
		root      string
		repoID    string
		threshold float64
		quality   float64
		jsonOut   bool
		emitBody  bool
	)
	cmd := &cobra.Command{
		Use:   "compress",
		Short: "Distill rule corpora: drop redundant + near-duplicate sections",
		Long: `compress reduces the operational rule corpus rooted at --root into a
smaller behavioral representation. Exact-duplicate sections are dropped,
near-duplicates pruned via Jaccard shingle similarity, and retained
sections compacted (whitespace + consecutive line dedup).

The compressor never persists the result automatically — pass
--emit-body to include compacted bodies in the output. Token savings are
reported per document and as a corpus total. A quality floor (default
0.6) guards against over-aggressive compression: results below the
floor are reported with accepted=false so callers can fall back to the
original corpus.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
				root = wd
			}
			docs, err := rulesfs.LoadCorpus(root, repoID)
			if err != nil {
				return err
			}
			c := rules.NewCompressor(rules.CompressConfig{
				SimilarityThreshold: threshold,
				QualityFloor:        quality,
			}, nil)
			views := make([]compressView, 0, len(docs))
			for _, d := range docs {
				r := c.Compress(d)
				view := compressView{
					SourceID:         r.SourceID,
					Path:             d.Path,
					OriginalTokens:   r.OriginalTokens,
					CompressedTokens: r.CompressedTokens,
					QualityScore:     r.QualityScore,
					Accepted:         r.Accepted,
					Sections:         r.Sections,
				}
				if emitBody {
					view.Body = r.CompactedBody()
				}
				views = append(views, view)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Results []compressView `json:"results"`
				}{Results: views})
			}
			renderCompressText(cmd, views)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "repository root to scan (defaults to current working directory)")
	cmd.Flags().StringVar(&repoID, "repo-id", "", "opaque repository identifier prepended to SourceIDs")
	cmd.Flags().Float64Var(&threshold, "similarity", 0.85, "Jaccard similarity threshold for near-duplicate pruning (0 disables)")
	cmd.Flags().Float64Var(&quality, "quality-floor", 0.6, "minimum compression quality score (compressed/original) before result is accepted")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().BoolVar(&emitBody, "emit-body", false, "include compacted body text in output")
	return cmd
}

func renderCompressText(cmd *cobra.Command, views []compressView) {
	out := cmd.OutOrStdout()
	if len(views) == 0 {
		fmt.Fprintln(out, "no rule artifacts found")
		return
	}
	fmt.Fprintf(out, "%-40s %10s %10s %8s %8s\n", "SOURCE", "ORIGINAL", "COMPRESSED", "QUALITY", "OK")
	for _, r := range views {
		ok := "no"
		if r.Accepted {
			ok = "yes"
		}
		fmt.Fprintf(out, "%-40s %10d %10d %8.2f %8s\n",
			truncateRule(r.Path, 40), r.OriginalTokens, r.CompressedTokens, r.QualityScore, ok)
		dropped := 0
		for _, s := range r.Sections {
			if s.Dropped {
				dropped++
			}
		}
		if dropped > 0 {
			fmt.Fprintf(out, "    dropped: %d section(s)\n", dropped)
		}
	}
}

func newRulesConflictsCmd() *cobra.Command {
	var (
		root    string
		repoID  string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "Detect redundancy, drift, and anti-patterns across rule artifacts",
		Long: `conflicts ingests rule artifacts under --root and reports three
classes of finding:

  redundant     identical section bodies across sources
  drift         same anchor in multiple sources with diverging bodies
  anti_pattern  competing-incentive phrases (concise vs verbose, tdd
                vs test-after, etc.) inside or across sections

Findings carry section identifiers and trigger names but never raw
section bodies, so the output is safe to forward to OTLP collectors.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
				root = wd
			}
			docs, err := rulesfs.LoadCorpus(root, repoID)
			if err != nil {
				return err
			}
			findings := rules.DetectConflicts(docs, rules.ConflictOptions{})
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Findings []rules.Finding `json:"findings"`
				}{Findings: findings})
			}
			renderConflictsText(cmd, findings)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "repository root to scan (defaults to current working directory)")
	cmd.Flags().StringVar(&repoID, "repo-id", "", "opaque repository identifier prepended to SourceIDs")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	return cmd
}

func renderConflictsText(cmd *cobra.Command, findings []rules.Finding) {
	out := cmd.OutOrStdout()
	if len(findings) == 0 {
		fmt.Fprintln(out, "no conflicts detected")
		return
	}
	for _, f := range findings {
		fmt.Fprintf(out, "[%s] %s\n", f.Kind, f.Detail)
		for _, m := range f.Members {
			fmt.Fprintf(out, "  - %s\n", m)
		}
		if len(f.Triggers) > 0 {
			fmt.Fprintf(out, "  triggers: %v\n", f.Triggers)
		}
	}
}

func newRulesAnalyzeCmd() *cobra.Command {
	var (
		root     string
		repoID   string
		provider string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Measure token cost of rule artifacts",
		Long: `analyze ingests operational rule artifacts under --root and reports
per-document and per-section token cost. Use --json for machine-readable
output suitable for piping into downstream tooling.

The default discovery set covers CLAUDE.md, AGENTS.md, .cursor/rules/**,
*.mcp.{yaml,yml,json}, and docs/conventions/*.md. Hidden directories
(other than .cursor) and well-known vendor trees (node_modules, vendor)
are skipped.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
				root = wd
			}
			prov, err := parseProvider(provider)
			if err != nil {
				return err
			}
			docs, err := rulesfs.LoadCorpus(root, repoID)
			if err != nil {
				return err
			}
			res, err := rules.AnalyzeDocs(docs, rules.AnalysisOptions{
				Providers: []eventschema.Provider{prov},
			})
			if err != nil {
				return err
			}
			if jsonOut {
				out := struct {
					Documents       []rules.DocumentSummary `json:"documents"`
					DuplicateGroups map[string][]string     `json:"duplicate_groups,omitempty"`
				}{Documents: res.Documents, DuplicateGroups: res.DuplicateGroups}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			}
			renderAnalyzeText(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "repository root to scan (defaults to current working directory)")
	cmd.Flags().StringVar(&repoID, "repo-id", "", "opaque repository identifier prepended to SourceIDs")
	cmd.Flags().StringVar(&provider, "provider", "openai", "tokenizer provider (openai|anthropic|gemini)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	return cmd
}

func parseProvider(s string) (eventschema.Provider, error) {
	switch s {
	case "", "openai":
		return eventschema.ProviderOpenAI, nil
	case "anthropic":
		return eventschema.ProviderAnthropic, nil
	case "gemini":
		return eventschema.ProviderGemini, nil
	default:
		return "", fmt.Errorf("unknown provider %q (want openai|anthropic|gemini)", s)
	}
}

func renderAnalyzeText(cmd *cobra.Command, res *rules.AnalysisResult) {
	out := cmd.OutOrStdout()
	if len(res.Documents) == 0 {
		fmt.Fprintln(out, "no rule artifacts found")
		return
	}
	sort.SliceStable(res.Documents, func(i, j int) bool {
		return res.Documents[i].TotalTokens > res.Documents[j].TotalTokens
	})
	fmt.Fprintf(out, "%-40s %-18s %8s %8s %6s\n", "SOURCE", "KIND", "TOKENS", "CHARS", "SECT")
	for _, d := range res.Documents {
		fmt.Fprintf(out, "%-40s %-18s %8d %8d %6d\n",
			truncateRule(d.Path, 40), string(d.Source), d.TotalTokens, d.TotalChars, d.Sections)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Top sections by token cost:")
	fmt.Fprintf(out, "  %-48s %8s %8s %10s\n", "SECTION", "TOKENS", "CHARS", "TOK/KCHAR")
	for _, d := range res.Documents {
		for _, s := range d.TopSections {
			anchor := s.Anchor
			if anchor == "" {
				anchor = "(preamble)"
			}
			fmt.Fprintf(out, "  %-48s %8d %8d %10.1f\n",
				truncateRule(d.Path+"#"+anchor, 48), s.TokenCount, s.CharCount, s.TokensPerKChar)
		}
	}
	if len(res.DuplicateGroups) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Duplicate section bodies: %d group(s)\n", len(res.DuplicateGroups))
		for h, ids := range res.DuplicateGroups {
			fmt.Fprintf(out, "  %s\n", h)
			for _, id := range ids {
				fmt.Fprintf(out, "    - %s\n", id)
			}
		}
	}
}

func truncateRule(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
