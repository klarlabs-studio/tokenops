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
	cmd.AddCommand(newVendorUsageEnableCmd())
	return cmd
}

// vendorUsageEnableFlags is the union of every source's tunables. Per-source
// validation rejects irrelevant flags; the union shape keeps the cobra
// surface flat and matches how an operator would Tab-complete the command.
type vendorUsageEnableFlags struct {
	sessionKey  string
	orgID       string
	cookie      string
	userID      string
	adminKey    string
	bucketWidth string
	oauthToken  string
	root        string
	interval    time.Duration
	disable     bool
	configPath  string
}

// vendorUsageSources lists the keys accepted as the positional argument to
// `tokenops vendor-usage enable`. Kept centralised so the help text, the
// error message on a bad key, and the test matrix all share one source of
// truth.
var vendorUsageSources = []string{
	"anthropic-cookie",
	"cursor",
	"github-copilot",
	"codex-jsonl",
	"claude-code-jsonl",
	"anthropic-admin",
}

// envSecret picks up secrets from the environment so operators can avoid
// putting them in shell history. The flag value still wins if both are set.
func envSecret(flag, envKey string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv(envKey)
}

// newVendorUsageEnableCmd writes a vendor-usage source's config block to
// the active config file so the operator does not hand-edit YAML. Secrets
// (session keys, cookies, admin keys, OAuth tokens) accept either a flag
// or an env-var fallback — env-var is the recommended path for CI / shared
// shell history.
func newVendorUsageEnableCmd() *cobra.Command {
	f := &vendorUsageEnableFlags{}
	cmd := &cobra.Command{
		Use:   "enable <source>",
		Short: "Enable a vendor-usage source and write its config block",
		Long: `enable flips vendor_usage.<source>.enabled to true (or false with
--disable) and persists any provided secrets/paths to the active config
file. Restart the daemon to pick up the change.

Sources:

  anthropic-cookie    claude.ai sessionKey scraper — surfaces Claude Max
                      5h + 7d + 7d-opus utilization %. Required: --session-key
                      (or env TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY).
  cursor              cursor.com /api/usage cookie scraper. Required: --cookie
                      (or env TOKENOPS_CURSOR_COOKIE) and --user-id.
  github-copilot      api.github.com/copilot_internal/user quota poller.
                      Auto-discovers the OAuth token from
                      ~/.config/github-copilot; pass --oauth-token to override.
  codex-jsonl         ~/.codex/sessions/**/*.jsonl reader. No secrets;
                      --root overrides the default scan root.
  claude-code-jsonl   ~/.claude/projects/**/*.jsonl reader. No secrets;
                      --root overrides the default scan root.
  anthropic-admin     Anthropic Admin API usage report poller. Required:
                      --admin-key (or env TOKENOPS_ANTHROPIC_ADMIN_KEY).

Examples:

  TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY=sk-ant-... \
    tokenops vendor-usage enable anthropic-cookie
  tokenops vendor-usage enable cursor --cookie ey... --user-id 123abc
  tokenops vendor-usage enable github-copilot
  tokenops vendor-usage enable codex-jsonl --interval 1m
  tokenops vendor-usage enable anthropic-cookie --disable`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVendorUsageEnable(cmd, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.sessionKey, "session-key", "", "claude.ai sessionKey cookie (anthropic-cookie); env TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY")
	cmd.Flags().StringVar(&f.orgID, "org-id", "", "Anthropic org_id (anthropic-cookie); auto-resolved on first scan when empty")
	cmd.Flags().StringVar(&f.cookie, "cookie", "", "WorkosCursorSessionToken cookie (cursor); env TOKENOPS_CURSOR_COOKIE")
	cmd.Flags().StringVar(&f.userID, "user-id", "", "Cursor user_id (cursor)")
	cmd.Flags().StringVar(&f.adminKey, "admin-key", "", "Anthropic admin key (anthropic-admin); env TOKENOPS_ANTHROPIC_ADMIN_KEY")
	cmd.Flags().StringVar(&f.bucketWidth, "bucket-width", "", "anthropic-admin bucket width (1m|1h|1d); empty keeps current")
	cmd.Flags().StringVar(&f.oauthToken, "oauth-token", "", "GitHub Copilot OAuth token (github-copilot); env TOKENOPS_COPILOT_OAUTH_TOKEN. Empty = auto-discover")
	cmd.Flags().StringVar(&f.root, "root", "", "filesystem root for jsonl readers (codex-jsonl, claude-code-jsonl); empty = default")
	cmd.Flags().DurationVar(&f.interval, "interval", 0, "poll interval; zero keeps the existing or default")
	cmd.Flags().BoolVar(&f.disable, "disable", false, "set enabled=false instead of true; clears no secrets")
	cmd.Flags().StringVar(&f.configPath, "config-path", "", "override config file path")
	return cmd
}

func runVendorUsageEnable(cmd *cobra.Command, source string, f *vendorUsageEnableFlags) error {
	path, err := resolveMutableConfigPath(f.configPath)
	if err != nil {
		return err
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	enabled := !f.disable

	switch source {
	case "anthropic-cookie":
		key := envSecret(f.sessionKey, "TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY")
		if enabled && key == "" {
			return fmt.Errorf("anthropic-cookie requires --session-key or TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY (paste from claude.ai devtools → Application → Cookies → sessionKey)")
		}
		cfg.VendorUsage.AnthropicCookie.Enabled = enabled
		if key != "" {
			cfg.VendorUsage.AnthropicCookie.SessionKey = key
		}
		if f.orgID != "" {
			cfg.VendorUsage.AnthropicCookie.OrgID = f.orgID
		}
		if f.interval > 0 {
			cfg.VendorUsage.AnthropicCookie.Interval = f.interval
		}
	case "cursor":
		cookie := envSecret(f.cookie, "TOKENOPS_CURSOR_COOKIE")
		if enabled && (cookie == "" || f.userID == "") {
			return fmt.Errorf("cursor requires --cookie (or TOKENOPS_CURSOR_COOKIE) AND --user-id (paste both from cursor.com devtools)")
		}
		cfg.VendorUsage.Cursor.Enabled = enabled
		if cookie != "" {
			cfg.VendorUsage.Cursor.Cookie = cookie
		}
		if f.userID != "" {
			cfg.VendorUsage.Cursor.UserID = f.userID
		}
		if f.interval > 0 {
			cfg.VendorUsage.Cursor.Interval = f.interval
		}
	case "github-copilot":
		token := envSecret(f.oauthToken, "TOKENOPS_COPILOT_OAUTH_TOKEN")
		cfg.VendorUsage.GitHubCopilot.Enabled = enabled
		if token != "" {
			cfg.VendorUsage.GitHubCopilot.OAuthToken = token
		}
		if f.interval > 0 {
			cfg.VendorUsage.GitHubCopilot.Interval = f.interval
		}
	case "codex-jsonl":
		cfg.VendorUsage.CodexJSONL.Enabled = enabled
		if f.root != "" {
			cfg.VendorUsage.CodexJSONL.Root = f.root
		}
		if f.interval > 0 {
			cfg.VendorUsage.CodexJSONL.Interval = f.interval
		}
	case "claude-code-jsonl":
		cfg.VendorUsage.ClaudeCodeJSONL.Enabled = enabled
		if f.root != "" {
			cfg.VendorUsage.ClaudeCodeJSONL.Root = f.root
		}
		if f.interval > 0 {
			cfg.VendorUsage.ClaudeCodeJSONL.Interval = f.interval
		}
	case "anthropic-admin":
		key := envSecret(f.adminKey, "TOKENOPS_ANTHROPIC_ADMIN_KEY")
		if enabled && key == "" && cfg.VendorUsage.Anthropic.AdminKey == "" {
			return fmt.Errorf("anthropic-admin requires --admin-key or TOKENOPS_ANTHROPIC_ADMIN_KEY (mint an sk-ant-admin-* key in the Claude Console)")
		}
		cfg.VendorUsage.Anthropic.Enabled = enabled
		if key != "" {
			cfg.VendorUsage.Anthropic.AdminKey = key
		}
		if f.bucketWidth != "" {
			cfg.VendorUsage.Anthropic.BucketWidth = f.bucketWidth
		}
		if f.interval > 0 {
			cfg.VendorUsage.Anthropic.Interval = f.interval
		}
	default:
		return fmt.Errorf("unknown source %q; valid: %v", source, vendorUsageSources)
	}

	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	action := "enabled"
	if f.disable {
		action = "disabled"
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"%s vendor_usage.%s\nwrote %s\nnext: restart the daemon, then `tokenops vendor-usage status`\n",
		action, sourceConfigKey(source), path,
	)
	return nil
}

// sourceConfigKey maps the positional source argument to the YAML key the
// daemon reads. The argument uses kebab-case for typability; the config key
// uses snake_case for YAML readability.
func sourceConfigKey(source string) string {
	switch source {
	case "anthropic-cookie":
		return "anthropic_cookie"
	case "github-copilot":
		return "github_copilot"
	case "codex-jsonl":
		return "codex_jsonl"
	case "claude-code-jsonl":
		return "claude_code_jsonl"
	case "anthropic-admin":
		return "anthropic"
	case "cursor":
		return "cursor"
	}
	return source
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
					{
						Name:        "anthropic_cookie",
						SourceTag:   "anthropic-cookie",
						Enabled:     cfg.VendorUsage.AnthropicCookie.Enabled,
						EventsInWin: counts["anthropic-cookie"],
						ConfigHint:  configHintAnthropicCookie(cfg.VendorUsage.AnthropicCookie),
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

func configHintAnthropicCookie(cfg config.AnthropicCookieUsageConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.anthropic_cookie.{enabled, session_key} — paste sessionKey from claude.ai devtools (Application → Cookies). RECOMMENDED for Claude Max users — only source of the official 7-day utilization %"
	}
	if cfg.SessionKey == "" {
		return "vendor_usage.anthropic_cookie enabled but session_key missing — paste from claude.ai devtools"
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
