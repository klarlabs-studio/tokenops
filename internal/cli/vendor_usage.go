package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/config"
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
	return cmd
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
						Name:        "claude_code_stats_cache",
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
		return ""
	}
	return "set vendor_usage.claude_code.enabled: true in config.yaml"
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
	fmt.Fprintln(out, "  events_in_window > 0 for claude-code-stats-cache    -> medium")
	fmt.Fprintln(out, "  otherwise                                           -> low (mcp pings only)")
}
