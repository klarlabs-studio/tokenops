package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecode"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudestatusline"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
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
	"go.klarlabs.de/tokenops/internal/infra/codexsettings"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
	claudeoauthapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/claudecodeoauth"
	claudeai "go.klarlabs.de/tokenops/internal/infra/vendorusage/claudeusagemeter"
	codexappserverproc "go.klarlabs.de/tokenops/internal/infra/vendorusage/codexappserver"
	copilotapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/copilot"
	cursorapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/cursor"
	fireworksapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/fireworks"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// StartVendorUsagePollers composes the configured local and provider
// usage readers and registers every recurring source with sup.
//
// It is composition-root wiring: each poller is a domain type built from
// configuration and an infrastructure client, and the daemon only needs
// them started. Keeping that wiring here is what keeps the daemon, which
// is also the local API adapter, from importing every vendor-usage domain
// package (see internal/archlint/capability_test.go).
//
// claudeCodeBaseURLAt answers where Claude Code was pointed at a moment;
// the Claude Code JSONL reader uses it to attribute each turn to the
// endpoint that served it.
func StartVendorUsagePollers(
	cfg config.Config,
	bus events.Bus,
	sourceHealth *freshness.Registry,
	claudeCodeBaseURLAt func(time.Time) string,
	sup *lifecycle.Supervisor,
	logger *slog.Logger,
) {
	if cfg.VendorUsage.ClaudeCode.Enabled {
		p := claudecode.NewPoller(bus, claudecode.PollerOptions{
			Path: cfg.VendorUsage.ClaudeCode.Path, Interval: cfg.VendorUsage.ClaudeCode.Interval,
			Logger: logger, CostSource: PlanCostSource(cfg, eventschema.ProviderAnthropic),
		})
		sup.Go("claude-code-stats-cache", p.Run)
		logger.Warn("claude-code stats cache poller is DEPRECATED — switch to vendor_usage.claude_code_jsonl for live per-turn data",
			"interval", cfg.VendorUsage.ClaudeCode.Interval, "path", cfg.VendorUsage.ClaudeCode.Path)
	}
	if cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
		p := claudecodejsonl.NewPoller(bus, claudecodejsonl.PollerOptions{
			Root: cfg.VendorUsage.ClaudeCodeJSONL.Root, Interval: cfg.VendorUsage.ClaudeCodeJSONL.Interval,
			Logger: logger, CostSource: PlanCostSource(cfg, eventschema.ProviderAnthropic),
			BaseURLAt: claudeCodeBaseURLAt,
		})
		sup.Go("claude-code-jsonl", p.Run)
		logger.Info("claude-code jsonl poller live", "interval", cfg.VendorUsage.ClaudeCodeJSONL.Interval, "root", cfg.VendorUsage.ClaudeCodeJSONL.Root)
	}
	if cfg.VendorUsage.CodexJSONL.Enabled {
		p := codexjsonl.NewPoller(bus, codexjsonl.PollerOptions{
			Root: cfg.VendorUsage.CodexJSONL.Root, Interval: cfg.VendorUsage.CodexJSONL.Interval,
			Logger: logger, CostSource: PlanCostSource(cfg, eventschema.ProviderOpenAI),
			ProviderBaseURL: codexsettings.ProviderBaseURL,
		})
		sup.Go("codex-jsonl", p.Run)
		logger.Info("codex jsonl poller live", "interval", cfg.VendorUsage.CodexJSONL.Interval, "root", cfg.VendorUsage.CodexJSONL.Root)
	}
	if cfg.VendorUsage.OpenCode.Enabled {
		p := opencode.NewPoller(bus, opencode.PollerOptions{
			Root: cfg.VendorUsage.OpenCode.Root, Store: opencodestore.Store{}, Interval: cfg.VendorUsage.OpenCode.Interval, Logger: logger,
		})
		sup.Go("opencode", p.Run)
		logger.Info("opencode poller live", "interval", cfg.VendorUsage.OpenCode.Interval, "root", cfg.VendorUsage.OpenCode.Root)
	}
	if cfg.VendorUsage.GeminiCLI.Enabled {
		p := geminicli.NewPoller(bus, geminicli.PollerOptions{
			Root: cfg.VendorUsage.GeminiCLI.Root, Interval: cfg.VendorUsage.GeminiCLI.Interval,
			Logger: logger, CostSource: PlanCostSource(cfg, eventschema.ProviderGemini),
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
		PlanCovered: PlanCostSource(cfg, eventschema.ProviderCursor) == eventschema.CostSourcePlanIncluded,
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
			NewClient: claudeai.NewSessionClient,
		})
		sup.Go("claude-usage-meter", p.Run)
		logger.Info("claude-usage-meter usage poller live", "interval", cfg.VendorUsage.ClaudeUsageMeter.Interval)
	}
	if cs := cfg.VendorUsage.CodexAppServer; cs.On() {
		home, _ := os.UserHomeDir()
		bin, found := cs.Path, cs.Path != ""
		if !found {
			bin, found = codexappserverproc.Locate(home)
		}
		if found {
			p := codexappserver.NewPoller(bus, codexappserver.PollerOptions{
				Dial: codexappserverproc.Command(bin), Interval: cs.Interval,
				Health: sourceHealth.For(codexappserver.SourceTag), Logger: logger,
			})
			sup.Go(codexappserver.SourceTag, p.Run)
			logger.Info("codex-app-server rate-limit poller live", "codex", bin)
		}
	}
	if oc := cfg.VendorUsage.ClaudeCodeOAuth; oc.Enabled {
		home, _ := os.UserHomeDir()
		p := claudecodeoauth.NewPoller(bus, claudecodeoauth.PollerOptions{
			Stores:   claudeoauthapi.Stores(home, oc.Keychain),
			Client:   claudeoauthapi.Client{UserAgent: "tokenops/" + version.Version},
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
			Readers: accountsapi.Readers(), Gateways: accountsapi.Gateways(),
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

// PlanCostSource returns the CostSource vendor-usage pollers stamp on
// emitted events: plan_included when the operator bound a flat-rate
// plan to the provider (config plans:), metered (empty) otherwise.
// Without the stamp, the analytics recompute would price
// subscription-covered usage at API list rates and budget alerts would
// fire on spend that never billed.
func PlanCostSource(cfg config.Config, provider eventschema.Provider) eventschema.CostSource {
	if cfg.PlanCovers(string(provider)) {
		return eventschema.CostSourcePlanIncluded
	}
	return ""
}
