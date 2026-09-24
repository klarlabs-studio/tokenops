package daemon

import (
	"context"
	"log/slog"
	"os"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecode"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	claudeusagemeter "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	copilotusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot"
	cursorusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
	cursorturnspoll "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursorturns"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// startVendorUsagePollers composes configured local and provider usage readers.
// Every recurring source is registered with the daemon supervisor.
func startVendorUsagePollers(
	cfg config.Config,
	bus *events.AsyncBus,
	sourceHealth *freshness.Registry,
	sup *lifecycle.Supervisor,
	logger *slog.Logger,
) {
	if cfg.VendorUsage.ClaudeCode.Enabled {
		p := claudecode.NewPoller(bus, claudecode.PollerOptions{
			Path: cfg.VendorUsage.ClaudeCode.Path, Interval: cfg.VendorUsage.ClaudeCode.Interval,
			Logger: logger, CostSource: planCostSource(cfg, eventschema.ProviderAnthropic),
		})
		sup.Go("claude-code-stats-cache", p.Run)
		logger.Warn("claude-code stats cache poller is DEPRECATED — switch to vendor_usage.claude_code_jsonl for live per-turn data",
			"interval", cfg.VendorUsage.ClaudeCode.Interval, "path", cfg.VendorUsage.ClaudeCode.Path)
	}
	if cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
		p := claudecodejsonl.NewPoller(bus, claudecodejsonl.PollerOptions{
			Root: cfg.VendorUsage.ClaudeCodeJSONL.Root, Interval: cfg.VendorUsage.ClaudeCodeJSONL.Interval,
			Logger: logger, CostSource: planCostSource(cfg, eventschema.ProviderAnthropic),
		})
		sup.Go("claude-code-jsonl", p.Run)
		logger.Info("claude-code jsonl poller live", "interval", cfg.VendorUsage.ClaudeCodeJSONL.Interval, "root", cfg.VendorUsage.ClaudeCodeJSONL.Root)
	}
	if cfg.VendorUsage.CodexJSONL.Enabled {
		p := codexjsonl.NewPoller(bus, codexjsonl.PollerOptions{
			Root: cfg.VendorUsage.CodexJSONL.Root, Interval: cfg.VendorUsage.CodexJSONL.Interval,
			Logger: logger, CostSource: planCostSource(cfg, eventschema.ProviderOpenAI),
		})
		sup.Go("codex-jsonl", p.Run)
		logger.Info("codex jsonl poller live", "interval", cfg.VendorUsage.CodexJSONL.Interval, "root", cfg.VendorUsage.CodexJSONL.Root)
	}
	if cfg.VendorUsage.OpenCode.Enabled {
		p := opencode.NewPoller(bus, opencode.PollerOptions{
			Root: cfg.VendorUsage.OpenCode.Root, Interval: cfg.VendorUsage.OpenCode.Interval, Logger: logger,
		})
		sup.Go("opencode", p.Run)
		logger.Info("opencode poller live", "interval", cfg.VendorUsage.OpenCode.Interval, "root", cfg.VendorUsage.OpenCode.Root)
	}
	if cfg.VendorUsage.Cursor.Enabled {
		p := cursorusage.NewPoller(bus, cursorusage.PollerOptions{
			Health: sourceHealth.For("cursor-web"), Cookie: cfg.VendorUsage.Cursor.Cookie,
			UserID: cfg.VendorUsage.Cursor.UserID, Interval: cfg.VendorUsage.Cursor.Interval, Logger: logger,
		})
		sup.Go("cursor-web", p.Run)
		logger.Info("cursor usage poller live", "interval", cfg.VendorUsage.Cursor.Interval, "user_id", cfg.VendorUsage.Cursor.UserID)
	}

	// Cursor's stop hook records per-turn consumption. Reading an empty ledger
	// is cheap and lets the hook work without additional daemon configuration.
	p := cursorturnspoll.NewPoller(bus, cursorturnspoll.PollerOptions{
		PlanCovered: planCostSource(cfg, eventschema.ProviderCursor) == eventschema.CostSourcePlanIncluded,
		Logger:      logger,
	})
	sup.Go("cursor-hook", p.Run)

	if cfg.VendorUsage.ClaudeUsageMeter.Enabled {
		p := claudeusagemeter.NewPoller(bus, claudeusagemeter.PollerOptions{
			Health: sourceHealth.For("claude-usage-meter"), SessionKey: cfg.VendorUsage.ClaudeUsageMeter.SessionKey,
			OrgID: cfg.VendorUsage.ClaudeUsageMeter.OrgID, Interval: cfg.VendorUsage.ClaudeUsageMeter.Interval,
			Logger: logger, Cookies: browserSessionSource(cfg.VendorUsage.ClaudeUsageMeter),
		})
		sup.Go("claude-usage-meter", p.Run)
		logger.Info("claude-usage-meter usage poller live", "interval", cfg.VendorUsage.ClaudeUsageMeter.Interval)
	}
	if cfg.VendorUsage.Anthropic.Enabled {
		client := anthropic.NewAdminClient(cfg.VendorUsage.Anthropic.AdminKey)
		p := anthropic.NewPoller(client, bus, anthropic.PollerOptions{
			Health: sourceHealth.For("vendor-usage-anthropic"), AdminKey: cfg.VendorUsage.Anthropic.AdminKey,
			Interval:    cfg.VendorUsage.Anthropic.Interval,
			BucketWidth: anthropic.BucketWidth(cfg.VendorUsage.Anthropic.BucketWidth), Logger: logger,
		})
		sup.Go("vendor-usage-anthropic", p.Run)
		logger.Info("anthropic admin usage poller live", "interval", cfg.VendorUsage.Anthropic.Interval, "bucket_width", cfg.VendorUsage.Anthropic.BucketWidth)
	}
	if cfg.VendorUsage.GitHubCopilot.Enabled {
		p := copilotusage.NewPoller(bus, copilotusage.PollerOptions{
			Health: sourceHealth.For("github-copilot"), OAuthToken: cfg.VendorUsage.GitHubCopilot.OAuthToken,
			Interval: cfg.VendorUsage.GitHubCopilot.Interval, Logger: logger,
		})
		sup.Go("github-copilot", p.Run)
		logger.Info("github copilot usage poller live", "interval", cfg.VendorUsage.GitHubCopilot.Interval)
	}
}

func browserSessionSource(cfg config.ClaudeUsageMeterConfig) func(context.Context) (claudeusagemeter.Session, error) {
	if !cfg.FromBrowser {
		return nil
	}
	return func(ctx context.Context) (claudeusagemeter.Session, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return claudeusagemeter.Session{}, err
		}
		cookies, browser, err := browsercookie.FindMany(ctx, home, "claude.ai", []string{"sessionKey", "cf_clearance"}, cfg.Browser, nil)
		if err != nil {
			return claudeusagemeter.Session{}, err
		}
		return claudeusagemeter.Session{
			Key: cookies["sessionKey"], Clearance: cookies["cf_clearance"],
			UserAgent: browser.UserAgent(), Browser: browser.Name,
		}, nil
	}
}
