package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/config"
	anthropicusage "github.com/felixgeelhaar/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

// newVendorUsageCmd assembles the `tokenops vendor-usage` command tree.
// One subcommand for now (status) — leaving a tree so future commands
// (`backfill`, `purge`, etc.) plug in without re-organising.
func newVendorUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vendor-usage",
		Short: "Inspect vendor-side usage pollers (Claude Code stats cache, Anthropic Admin API)",
	}
	cmd.AddCommand(newVendorUsageStatusCmd())
	cmd.AddCommand(newVendorUsageBackfillCmd())
	return cmd
}

// newVendorUsageBackfillCmd one-shot pulls historical Anthropic
// Admin API usage into the local store. Useful right after
// `tokenops vendor-usage anthropic.admin_key: …` lands in config —
// the operator sees previous days immediately instead of waiting
// for the 5-min poll loop to drip in only forward-looking buckets.
//
// Dedup is automatic: NewEnvelope hashes a deterministic ID per
// (bucket_start, model, api_key_id, workspace_id); store.Append
// silently no-ops on duplicates so backfill is idempotent and safe
// to run alongside a live poller.
func newVendorUsageBackfillCmd() *cobra.Command {
	var (
		hours   int
		dbPath  string
		jsonOut bool
		dryRun  bool
	)
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Pull historical Anthropic Admin usage into the local store (one-shot)",
		Long: `backfill calls /v1/organizations/usage_report/messages for the
window [now-hours, now] and inserts every (bucket, model) result as
a PromptEvent envelope tagged source=vendor-usage-anthropic.

Dedup is automatic — re-running the command (or running it while
the live poller is active) won't double-count, because envelope IDs
are deterministic.

Requires vendor_usage.anthropic.admin_key in config. The dry-run
flag prints what would be inserted without writing to the store.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(&rootFlags{})
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			key := cfg.VendorUsage.Anthropic.AdminKey
			if key == "" {
				return fmt.Errorf("vendor_usage.anthropic.admin_key is unset; mint an sk-ant-admin-* key in the Claude Console")
			}
			db, err := resolveAuditDB(&rootFlags{}, dbPath)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := sqlite.Open(ctx, db, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = store.Close() }()
			client := anthropicusage.NewAdminClient(key)
			now := time.Now().UTC()
			req := anthropicusage.MessagesUsageRequest{
				StartingAt:  now.Add(-time.Duration(hours) * time.Hour),
				EndingAt:    now,
				BucketWidth: anthropicusage.BucketWidthHour,
				GroupBy:     []string{"model"},
			}
			resp, err := client.MessagesUsage(ctx, req)
			if err != nil {
				return fmt.Errorf("fetch usage: %w", err)
			}
			var inserted, skipped int
			for _, bucket := range resp.Data {
				for _, r := range bucket.Results {
					env, ok := anthropicusage.NewEnvelope(bucket.StartingAt, bucket.EndingAt, r)
					if !ok {
						skipped++
						continue
					}
					if dryRun {
						inserted++
						continue
					}
					if err := store.Append(ctx, env); err != nil {
						return fmt.Errorf("append envelope %s: %w", env.ID, err)
					}
					inserted++
				}
			}
			report := map[string]any{
				"hours":    hours,
				"buckets":  len(resp.Data),
				"inserted": inserted,
				"skipped":  skipped,
				"dry_run":  dryRun,
				"db":       db,
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backfilled last %dh: %d envelopes %s, %d zero-token rows skipped\n",
				hours, inserted, dryRunLabel(dryRun), skipped)
			fmt.Fprintf(cmd.OutOrStdout(), "Store: %s\n", db)
			return nil
		},
	}
	cmd.Flags().IntVar(&hours, "hours", 168, "lookback window in hours (max 168 for hourly buckets per Admin API cap)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be inserted without writing to the store")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	return cmd
}

func dryRunLabel(dry bool) string {
	if dry {
		return "would-insert (dry run)"
	}
	return "inserted"
}

// newVendorUsageStatusCmd surfaces what each vendor-usage source has
// emitted into the event store. Reads config to show enabled/disabled
// state, then counts source-tagged envelopes in the last window.
// Pure-offline: no HTTP calls into the running daemon, so it works
// even when the daemon is down.
func newVendorUsageStatusCmd() *cobra.Command {
	var (
		window  time.Duration
		jsonOut bool
		dbPath  string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show configured vendor-usage sources and their recent event counts",
		Long: `status reads ~/.config/tokenops/config.yaml plus the event store
(~/.tokenops/events.db) and reports per-source state:

  - claude-code-stats-cache: ~/.claude/stats-cache.json poller
  - vendor-usage-anthropic:  Anthropic Admin API poller

For each source it prints whether the config block is enabled and how
many envelopes landed in the configured window. Use --json for
machine-readable output.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(&rootFlags{})
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			db, err := resolveAuditDB(&rootFlags{}, dbPath)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := sqlite.Open(ctx, db, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = store.Close() }()
			now := time.Now().UTC()
			counts, err := store.CountBySource(ctx, now.Add(-window), now)
			if err != nil {
				return fmt.Errorf("count events: %w", err)
			}
			report := vendorUsageReport{
				Window: window.String(),
				Sources: []vendorUsageSource{
					{
						Name:        "claude_code_jsonl",
						SourceTag:   "claude-code-jsonl",
						Enabled:     cfg.VendorUsage.ClaudeCodeJSONL.Enabled,
						EventsInWin: counts["claude-code-jsonl"],
						ConfigHint:  configHintClaudeCodeJSONL(cfg.VendorUsage.ClaudeCodeJSONL.Enabled),
					},
					{
						Name:        "codex_jsonl",
						SourceTag:   "codex-jsonl",
						Enabled:     cfg.VendorUsage.CodexJSONL.Enabled,
						EventsInWin: counts["codex-jsonl"],
						ConfigHint:  configHintCodexJSONL(cfg.VendorUsage.CodexJSONL.Enabled),
					},
					{
						Name:        "claude_code_stats_cache (deprecated)",
						SourceTag:   "claude-code-stats-cache",
						Enabled:     cfg.VendorUsage.ClaudeCode.Enabled,
						EventsInWin: counts["claude-code-stats-cache"],
						ConfigHint:  configHintClaudeCode(cfg.VendorUsage.ClaudeCode.Enabled),
					},
					{
						Name:        "vendor_usage_anthropic",
						SourceTag:   "vendor-usage-anthropic",
						Enabled:     cfg.VendorUsage.Anthropic.Enabled,
						EventsInWin: counts["vendor-usage-anthropic"],
						ConfigHint:  configHintAnthropic(cfg.VendorUsage.Anthropic),
					},
					{
						Name:        "github_copilot",
						SourceTag:   "github-copilot",
						Enabled:     cfg.VendorUsage.GitHubCopilot.Enabled,
						EventsInWin: counts["github-copilot"],
						ConfigHint:  configHintCopilot(cfg.VendorUsage.GitHubCopilot),
					},
					{
						Name:        "cursor_web",
						SourceTag:   "cursor-web",
						Enabled:     cfg.VendorUsage.Cursor.Enabled,
						EventsInWin: counts["cursor-web"],
						ConfigHint:  configHintCursor(cfg.VendorUsage.Cursor),
					},
				},
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			renderVendorUsageText(cmd, report)
			return nil
		},
	}
	cmd.Flags().DurationVar(&window, "window", 24*time.Hour, "window over which to count source-tagged envelopes")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	return cmd
}

type vendorUsageReport struct {
	Window  string              `json:"window"`
	Sources []vendorUsageSource `json:"sources"`
}

type vendorUsageSource struct {
	Name        string `json:"name"`
	SourceTag   string `json:"source_tag"`
	Enabled     bool   `json:"enabled"`
	EventsInWin int64  `json:"events_in_window"`
	ConfigHint  string `json:"config_hint,omitempty"`
}

func configHintClaudeCode(enabled bool) string {
	if enabled {
		return "DEPRECATED — switch to vendor_usage.claude_code_jsonl"
	}
	return "deprecated; use vendor_usage.claude_code_jsonl instead"
}

func configHintClaudeCodeJSONL(enabled bool) string {
	if enabled {
		return ""
	}
	return "set vendor_usage.claude_code_jsonl.enabled: true (RECOMMENDED — live per-turn signal)"
}

func configHintCodexJSONL(enabled bool) string {
	if enabled {
		return ""
	}
	return "set vendor_usage.codex_jsonl.enabled: true (RECOMMENDED for Codex Plus/Pro users — surfaces OpenAI's official rate_limits 5h + weekly %)"
}

func configHintCopilot(cfg config.GitHubCopilotUsageConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.github_copilot.enabled: true (auto-discovers OAuth token from ~/.config/github-copilot)"
	}
	return ""
}

func configHintCursor(cfg config.CursorUsageConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.cursor.{enabled, cookie, user_id} — extract cookie from the Cursor IDE devtools (WorkosCursorSessionToken)"
	}
	if cfg.Cookie == "" || cfg.UserID == "" {
		return "vendor_usage.cursor enabled but cookie or user_id missing — paste WorkosCursorSessionToken + your user_id from cursor.com devtools"
	}
	return ""
}

func configHintAnthropic(cfg config.AnthropicUsageConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.anthropic.enabled: true + an sk-ant-admin-* key"
	}
	if cfg.AdminKey == "" {
		return "vendor_usage.anthropic.admin_key is empty; mint a key in the Claude Console"
	}
	return ""
}

func renderVendorUsageText(cmd *cobra.Command, r vendorUsageReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Vendor-usage status — window=%s\n\n", r.Window)
	fmt.Fprintf(out, "%-26s %-30s %-9s %-12s %s\n", "NAME", "SOURCE TAG", "ENABLED", "EVENTS(WIN)", "HINT")
	for _, s := range r.Sources {
		fmt.Fprintf(out, "%-26s %-30s %-9v %-12d %s\n",
			s.Name, s.SourceTag, s.Enabled, s.EventsInWin, s.ConfigHint)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Signal_quality classifier consumes these counts:")
	fmt.Fprintln(out, "  events_in_window > 0 for vendor-usage-anthropic     -> high")
	fmt.Fprintln(out, "  events_in_window > 0 for claude-code-jsonl          -> high (real per-turn)")
	fmt.Fprintln(out, "  events_in_window > 0 for claude-code-stats-cache    -> medium (deprecated)")
	fmt.Fprintln(out, "  otherwise                                           -> low (mcp pings only)")
}
