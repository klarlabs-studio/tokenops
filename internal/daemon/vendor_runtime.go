package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.klarlabs.de/tokenops/internal/infra/codexsettings"

	"go.klarlabs.de/tokenops/internal/infra/routehistory"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecode"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudestatusline"
	claudeusagemeter "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexappserver"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	copilotusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot"
	cursorusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
	cursorturnspoll "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursorturns"
	fireworksusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/geminicli"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
	copilotapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/copilot"
	cursorapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/cursor"
	fireworksapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/fireworks"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// startVendorUsagePollers composes configured local and provider usage readers.
// Every recurring source is registered with the daemon supervisor.
func startVendorUsagePollers(
	cfg config.Config,
	bus events.Bus,
	sourceHealth *freshness.Registry,
	routes *routehistory.Tracker,
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
			BaseURLAt: claudeCodeBaseURLAt(routes),
		})
		sup.Go("claude-code-jsonl", p.Run)
		logger.Info("claude-code jsonl poller live", "interval", cfg.VendorUsage.ClaudeCodeJSONL.Interval, "root", cfg.VendorUsage.ClaudeCodeJSONL.Root)
	}
	if cfg.VendorUsage.CodexJSONL.Enabled {
		p := codexjsonl.NewPoller(bus, codexjsonl.PollerOptions{
			Root: cfg.VendorUsage.CodexJSONL.Root, Interval: cfg.VendorUsage.CodexJSONL.Interval,
			Logger: logger, CostSource: planCostSource(cfg, eventschema.ProviderOpenAI),
			ProviderBaseURL: codexsettings.ProviderBaseURL,
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
	if cfg.VendorUsage.GeminiCLI.Enabled {
		p := geminicli.NewPoller(bus, geminicli.PollerOptions{
			Root: cfg.VendorUsage.GeminiCLI.Root, Interval: cfg.VendorUsage.GeminiCLI.Interval,
			Logger: logger, CostSource: planCostSource(cfg, eventschema.ProviderGemini),
		})
		sup.Go("gemini-cli", p.Run)
		logger.Info("gemini cli poller live", "interval", cfg.VendorUsage.GeminiCLI.Interval, "root", cfg.VendorUsage.GeminiCLI.Root)
	}
	if cfg.VendorUsage.Cursor.Enabled {
		p := cursorusage.NewPoller(bus, cursorusage.PollerOptions{
			Health: sourceHealth.For("cursor-web"), Cookie: cfg.VendorUsage.Cursor.Cookie,
			UserID: cfg.VendorUsage.Cursor.UserID, Interval: cfg.VendorUsage.Cursor.Interval, Logger: logger,
			NewClient: cursorapi.NewSource,
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

	// Claude Code reports its plan windows to the status line, which leaves
	// them in a file; reading a file that does not exist costs nothing.
	sl := claudestatusline.NewPoller(bus, claudestatusline.PollerOptions{Logger: logger})
	sup.Go("claude-code-statusline", sl.Run)

	if cfg.VendorUsage.ClaudeUsageMeter.Enabled {
		p := claudeusagemeter.NewPoller(bus, claudeusagemeter.PollerOptions{
			Health: sourceHealth.For("claude-usage-meter"), SessionKey: cfg.VendorUsage.ClaudeUsageMeter.SessionKey,
			Clearance: cfg.VendorUsage.ClaudeUsageMeter.Clearance, UserAgent: cfg.VendorUsage.ClaudeUsageMeter.UserAgent,
			BrowserHeaders: cfg.VendorUsage.ClaudeUsageMeter.BrowserHeaders,
			BrowserCookies: cfg.VendorUsage.ClaudeUsageMeter.BrowserCookies,
			OrgID:          cfg.VendorUsage.ClaudeUsageMeter.OrgID, Interval: cfg.VendorUsage.ClaudeUsageMeter.Interval,
			Logger: logger, Cookies: browserSessionSource(cfg.VendorUsage.ClaudeUsageMeter),
		})
		sup.Go("claude-usage-meter", p.Run)
		logger.Info("claude-usage-meter usage poller live", "interval", cfg.VendorUsage.ClaudeUsageMeter.Interval)
	}
	if cs := cfg.VendorUsage.CodexAppServer; cs.On() {
		home, _ := os.UserHomeDir()
		bin, found := cs.Path, cs.Path != ""
		if !found {
			bin, found = codexappserver.Locate(home)
		}
		if found {
			p := codexappserver.NewPoller(bus, codexappserver.PollerOptions{
				Dial: codexappserver.Command(bin), Interval: cs.Interval,
				Health: sourceHealth.For(codexappserver.SourceTag), Logger: logger,
			})
			sup.Go(codexappserver.SourceTag, p.Run)
			logger.Info("codex-app-server rate-limit poller live", "codex", bin)
		}
	}
	if oc := cfg.VendorUsage.ClaudeCodeOAuth; oc.Enabled {
		home, _ := os.UserHomeDir()
		p := claudecodeoauth.NewPoller(bus, claudecodeoauth.PollerOptions{
			Stores:   claudecodeoauth.Stores(home, oc.Keychain),
			Client:   claudecodeoauth.Client{UserAgent: "tokenops/" + version.Version},
			Interval: oc.Interval, Health: sourceHealth.For(claudecodeoauth.SourceTag), Logger: logger,
		})
		sup.Go(claudecodeoauth.SourceTag, p.Run)
		logger.Info("claude-code-oauth usage poller live", "keychain", oc.Keychain)
	}
	if cfg.VendorUsage.Anthropic.Enabled {
		client := anthropicapi.NewAdminClient(cfg.VendorUsage.Anthropic.AdminKey)
		p := anthropic.NewPoller(client, bus, anthropic.PollerOptions{
			Health: sourceHealth.For("vendor-usage-anthropic"), AdminKey: cfg.VendorUsage.Anthropic.AdminKey,
			Interval:    cfg.VendorUsage.Anthropic.Interval,
			BucketWidth: anthropic.BucketWidth(cfg.VendorUsage.Anthropic.BucketWidth), Logger: logger,
		})
		sup.Go("vendor-usage-anthropic", p.Run)
		logger.Info("anthropic admin usage poller live", "interval", cfg.VendorUsage.Anthropic.Interval, "bucket_width", cfg.VendorUsage.Anthropic.BucketWidth)
	}
	if cfg.VendorUsage.Fireworks.On() {
		p := fireworksusage.NewPoller(bus, fireworksusage.PollerOptions{
			Health: sourceHealth.For("fireworks-usage"), Interval: cfg.VendorUsage.Fireworks.Interval, Logger: logger,
			Client: &fireworksapi.Client{Key: fireworksapi.KeySource{Helper: claudesettings.APIKeyHelper, Fallback: fireworksFallbackKey}.Key},
		})
		sup.Go("fireworks-usage", p.Run)
	}
	if cfg.VendorUsage.Accounts.On() {
		p := accounts.NewPoller(bus, accounts.PollerOptions{
			Credentials: accountCredentials, Health: sourceHealth.For,
			Interval: cfg.VendorUsage.Accounts.Interval, Logger: logger,
		})
		sup.Go("vendor-accounts", p.Run)
	}
	if cfg.VendorUsage.GitHubCopilot.Enabled {
		p := copilotusage.NewPoller(bus, copilotusage.PollerOptions{
			Health: sourceHealth.For("github-copilot"), OAuthToken: cfg.VendorUsage.GitHubCopilot.OAuthToken,
			Interval: cfg.VendorUsage.GitHubCopilot.Interval, Logger: logger, NewClient: copilotapi.NewSource,
		})
		sup.Go("github-copilot", p.Run)
		logger.Info("github copilot usage poller live", "interval", cfg.VendorUsage.GitHubCopilot.Interval)
	}
}

// browserSessionSource re-reads the claude.ai session from the browser.
// It is wired for a pasted session too (ADR 0011): when claude.ai refuses
// it, the browser's newer session is the meter's own way back, and the
// pinned organization keeps another account's session from being metered
// in its place. browser: none turns it off.
func browserSessionSource(cfg config.ClaudeUsageMeterConfig) func(context.Context) (claudeusagemeter.Session, error) {
	if strings.EqualFold(cfg.Browser, config.BrowserNone) {
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
