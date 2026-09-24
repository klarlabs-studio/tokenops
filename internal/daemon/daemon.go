// Package daemon hosts the boot sequence shared by tokenopsd and the
// tokenops CLI start subcommand. It composes config, logger, proxy server,
// and graceful shutdown so callers do not duplicate lifecycle wiring.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/security/dashauth"
	"go.klarlabs.de/tokenops/internal/contexts/security/tlsmint"
	anthropicusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecode"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	claudeusagemeter "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	copilotusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot"
	cursorusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
	cursorturnspoll "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursorturns"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/domainevents"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/rulesfs"
	"go.klarlabs.de/tokenops/internal/otlp"
	"go.klarlabs.de/tokenops/internal/proxy"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Run boots the daemon with cfg and blocks until ctx is cancelled (e.g. by
// SIGINT/SIGTERM). The logger is built from cfg.Log; pass logWriter=nil to
// emit to os.Stderr.
func Run(ctx context.Context, cfg config.Config, logWriter io.Writer) error {
	if logWriter == nil {
		logWriter = os.Stderr
	}
	logger := observ.NewLogger(logWriter, cfg.Log.Level, cfg.Log.Format)
	return RunWithLogger(ctx, cfg, logger)
}

// RunWithLogger is Run with a caller-supplied slog.Logger.
func RunWithLogger(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger.Info("tokenops daemon starting",
		"version", version.Version,
		"commit", version.Commit,
		"listen", cfg.Listen,
	)

	// Composition root constructs the domain bus + counter + redactor
	// (and any other long-lived collaborators) once. Daemon never
	// allocates a fresh bus or counter — it consumes what bootstrap
	// hands back. Store opens later only when storage is enabled.
	earlyComponents, err := bootstrap.New(ctx, bootstrap.Options{
		Logger:      logger,
		OpenStore:   false,
		PricingPath: cfg.Pricing.Path,
	})
	if err != nil {
		return err
	}
	dbus := earlyComponents.DomainBus
	domainEventCounter := earlyComponents.EventCounter
	workflow.SetDomainBus(dbus)
	optimizer.SetDomainBus(dbus)
	rulesfs.SetDomainBus(dbus)
	budget.SetDomainBus(dbus)
	dbus.Subscribe("*", func(ev domainevents.Event) {
		logger.Debug("domain event", "kind", ev.Kind())
	})

	// JSONL persistence so late subscribers can replay history.
	var domainLog *domainevents.JSONLog
	if cfg.Storage.Enabled {
		eventsPath, _ := resolveStoragePath(cfg.Storage.Path)
		logPath := filepath.Join(filepath.Dir(eventsPath), "domain-events.jsonl")
		if l, err := domainevents.NewJSONLog(logPath); err == nil {
			domainLog = l
			// Hydrate the in-memory counter from prior runs so dashboards
			// see continuity across restarts. Lenient mode skips bad
			// lines instead of aborting.
			if skipped, rerr := domainevents.ReplayLenient(logPath, func(r domainevents.Record) error {
				dbus.Publish(domainevents.NewReplayed(r.Kind, r.At))
				return nil
			}); rerr != nil {
				logger.Warn("domain event log replay", "err", rerr, "skipped", skipped)
			}
			domainLog.Attach(dbus, nil)
			logger.Info("domain event log ready", "path", logPath)
		} else {
			logger.Warn("domain event log unavailable", "err", err)
		}
	}

	// Async dispatch with bounded queue isolates the publisher hot
	// path from any slow subscriber. Started AFTER subscribers wire so
	// the worker sees them on first dispatch.
	dbus.StartAsync(1024)

	routes, err := proxy.BuildProviderRoutes(cfg.Providers)
	if err != nil {
		return fmt.Errorf("provider routes: %w", err)
	}

	// Every reader reports its own success and failure here, so a status
	// surface can tell a poller being refused apart from a vendor nobody
	// is using. Both produce no events; only the poll record separates
	// them.
	sourceHealth := freshness.NewRegistry()

	// Every long-running subsystem runs under the supervisor rather than
	// a bare `go func()`. Nine of them did, and nothing waited for any of
	// them: shutdown returned while pollers were mid-request, a poller
	// that died logged one line and was never mentioned again, and
	// "which subsystems are up" had no answer at all.
	sup := lifecycle.New(ctx, logger)

	opts := []proxy.Option{
		proxy.WithLogger(logger),
		proxy.WithShutdownTimeout(cfg.Shutdown.Timeout),
		proxy.WithProviderRoutes(routes),
		proxy.WithEventCounts(domainEventCounter.Counts),
		proxy.WithEventSpans(func() map[string]proxy.EventSpan {
			spans := domainEventCounter.Spans()
			out := make(map[string]proxy.EventSpan, len(spans))
			for k, v := range spans {
				out[k] = proxy.EventSpan{First: v.First, Last: v.Last}
			}
			return out
		}),
	}
	if cfg.Resilience.Enabled {
		opts = append(opts, proxy.WithResilience(proxy.ResilienceConfig{
			FirstByteTimeout: cfg.Resilience.FirstByteTimeout,
			IdleTimeout:      cfg.Resilience.IdleTimeout,
			TotalTimeout:     cfg.Resilience.TotalTimeout,
			FailureThreshold: cfg.Resilience.FailureThreshold,
		}))
		logger.Info("resilience enabled",
			"first_byte_timeout", cfg.Resilience.FirstByteTimeout,
			"idle_timeout", cfg.Resilience.IdleTimeout,
			"total_timeout", cfg.Resilience.TotalTimeout,
			"failure_threshold", cfg.Resilience.FailureThreshold,
		)
	}
	if cfg.TLS.Enabled {
		certDir, err := resolveCertDir(cfg.TLS.CertDir)
		if err != nil {
			return fmt.Errorf("tls cert dir: %w", err)
		}
		bundle, err := tlsmint.EnsureBundle(certDir, tlsmint.Options{
			Hostnames: cfg.TLS.Hostnames,
		})
		if err != nil {
			return fmt.Errorf("tls bundle: %w", err)
		}
		logger.Info("tls bundle ready",
			"cert_dir", bundle.Dir,
			"leaf_not_after", bundle.LeafCert.NotAfter,
		)
		opts = append(opts, proxy.WithTLS(bundle.TLSConfig()))
	}

	var (
		store   *sqlite.Store
		bus     *events.AsyncBus
		dashTok string
	)
	components := earlyComponents
	if cfg.Storage.Enabled {
		path, err := resolveStoragePath(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("storage path: %w", err)
		}
		if err := components.OpenStoreAt(ctx, path); err != nil {
			return err
		}
		store = components.Store

		// Audit recorder subscribes to security-relevant domain events.
		// Wired here (after the store opens) rather than at the dbus
		// init block above because audit requires persistence.
		auditSub := audit.Subscribe(dbus, audit.NewRecorder(store), logger, "daemon")
		if auditSub != nil {
			opts = append(opts, proxy.WithAuditDrops(auditSub.DroppedCount))
			defer auditSub.Close()
		}

		var sinks []events.Sink
		sinks = append(sinks, store)

		if cfg.OTel.Enabled {
			expOpts := otlp.Options{
				Endpoint:       cfg.OTel.Endpoint,
				Headers:        cfg.OTel.Headers,
				ServiceName:    cfg.OTel.ServiceName,
				ServiceVersion: cfg.OTel.ServiceVersion,
				Logger:         logger,
			}
			if cfg.OTel.RedactEnabled() {
				expOpts.Redactor = earlyComponents.Redactor
			}
			exporter, err := otlp.New(expOpts)
			if err != nil {
				return fmt.Errorf("otlp exporter: %w", err)
			}
			sinks = append(sinks, exporter)
			logger.Info("otlp exporter ready",
				"endpoint", cfg.OTel.Endpoint,
				"redact", cfg.OTel.RedactEnabled(),
			)
		}

		// Plan stamping wraps the sink so every emitter (pollers, proxy
		// observer, future sources) inherits the plan_included contract
		// without per-emitter wiring.
		bus = events.NewAsync(newPlanStampSink(events.NewMultiSink(sinks...), cfg), events.Options{Logger: logger})
		// Transitional Phase 6 bridge: domain events retain their legacy
		// subscribers while also entering the canonical envelope stream.
		domainevents.BridgeToEnvelopeBus(dbus, bus, logger)
		logger.Info("event store ready", "path", path)
		opts = append(opts,
			proxy.WithEventBus(bus),
			proxy.WithSourceFreshness(sourceFreshnessFn(cfg, store, sourceHealth, sup)),
			proxy.WithTokenizer(components.Tokenizers),
			proxy.WithCostEngine(components.Spend),
			// Rows the bus could not persist. Published so `tokenops
			// status` and the MCP status tool can warn about them while
			// the daemon is running, rather than the count first being
			// spoken aloud in a log line at shutdown.
			proxy.WithEventDrops(bus.DroppedCount),
		)

		// Optional vendor-usage pollers. Each one publishes envelopes
		// through the same bus the proxy uses, so vendor and proxy
		// signals end up in the same store with distinct Source tags
		// (signal_quality reads those tags to upgrade confidence).
		if cfg.VendorUsage.ClaudeCode.Enabled {
			p := claudecode.NewPoller(bus, claudecode.PollerOptions{
				Path:       cfg.VendorUsage.ClaudeCode.Path,
				Interval:   cfg.VendorUsage.ClaudeCode.Interval,
				Logger:     logger,
				CostSource: planCostSource(cfg, eventschema.ProviderAnthropic),
			})
			sup.Go("claude-code-stats-cache", p.Run)
			logger.Warn("claude-code stats cache poller is DEPRECATED — switch to vendor_usage.claude_code_jsonl for live per-turn data",
				"interval", cfg.VendorUsage.ClaudeCode.Interval,
				"path", cfg.VendorUsage.ClaudeCode.Path,
			)
		}
		if cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
			p := claudecodejsonl.NewPoller(bus, claudecodejsonl.PollerOptions{
				Root:       cfg.VendorUsage.ClaudeCodeJSONL.Root,
				Interval:   cfg.VendorUsage.ClaudeCodeJSONL.Interval,
				Logger:     logger,
				CostSource: planCostSource(cfg, eventschema.ProviderAnthropic),
			})
			sup.Go("claude-code-jsonl", p.Run)
			logger.Info("claude-code jsonl poller live",
				"interval", cfg.VendorUsage.ClaudeCodeJSONL.Interval,
				"root", cfg.VendorUsage.ClaudeCodeJSONL.Root,
			)
		}
		if cfg.VendorUsage.CodexJSONL.Enabled {
			p := codexjsonl.NewPoller(bus, codexjsonl.PollerOptions{
				Root:       cfg.VendorUsage.CodexJSONL.Root,
				Interval:   cfg.VendorUsage.CodexJSONL.Interval,
				Logger:     logger,
				CostSource: planCostSource(cfg, eventschema.ProviderOpenAI),
			})
			sup.Go("codex-jsonl", p.Run)
			logger.Info("codex jsonl poller live",
				"interval", cfg.VendorUsage.CodexJSONL.Interval,
				"root", cfg.VendorUsage.CodexJSONL.Root,
			)
		}
		if cfg.VendorUsage.OpenCode.Enabled {
			// opencode is multi-provider; leave CostSource metered since no
			// single flat-rate plan covers its traffic.
			p := opencode.NewPoller(bus, opencode.PollerOptions{
				Root:     cfg.VendorUsage.OpenCode.Root,
				Interval: cfg.VendorUsage.OpenCode.Interval,
				Logger:   logger,
			})
			sup.Go("opencode", p.Run)
			logger.Info("opencode poller live",
				"interval", cfg.VendorUsage.OpenCode.Interval,
				"root", cfg.VendorUsage.OpenCode.Root,
			)
		}
		if cfg.VendorUsage.Cursor.Enabled {
			p := cursorusage.NewPoller(bus, cursorusage.PollerOptions{
				Health:   sourceHealth.For("cursor-web"),
				Cookie:   cfg.VendorUsage.Cursor.Cookie,
				UserID:   cfg.VendorUsage.Cursor.UserID,
				Interval: cfg.VendorUsage.Cursor.Interval,
				Logger:   logger,
			})
			sup.Go("cursor-web", p.Run)
			logger.Info("cursor usage poller live",
				"interval", cfg.VendorUsage.Cursor.Interval,
				"user_id", cfg.VendorUsage.Cursor.UserID,
			)
		}
		// Cursor's own endpoint reports plan consumption — a percentage,
		// not tokens. Its stop hook is the only place per-turn counts
		// exist, and coach-hook records them; this is what turns that
		// ledger into spend. Unconditional because the ledger is empty
		// until the hook is installed, so an operator who does not run
		// Cursor pays nothing for it.
		{
			p := cursorturnspoll.NewPoller(bus, cursorturnspoll.PollerOptions{
				PlanCovered: planCostSource(cfg, eventschema.ProviderCursor) == eventschema.CostSourcePlanIncluded,
				Logger:      logger,
			})
			sup.Go("cursor-hook", p.Run)
		}
		if cfg.VendorUsage.ClaudeUsageMeter.Enabled {
			p := claudeusagemeter.NewPoller(bus, claudeusagemeter.PollerOptions{
				Health:     sourceHealth.For("claude-usage-meter"),
				SessionKey: cfg.VendorUsage.ClaudeUsageMeter.SessionKey,
				OrgID:      cfg.VendorUsage.ClaudeUsageMeter.OrgID,
				Interval:   cfg.VendorUsage.ClaudeUsageMeter.Interval,
				Logger:     logger,
				Cookies:    browserSessionSource(cfg.VendorUsage.ClaudeUsageMeter),
			})
			sup.Go("claude-usage-meter", p.Run)
			logger.Info("claude-usage-meter usage poller live",
				"interval", cfg.VendorUsage.ClaudeUsageMeter.Interval,
			)
		}
		if cfg.VendorUsage.Anthropic.Enabled {
			client := anthropicusage.NewAdminClient(cfg.VendorUsage.Anthropic.AdminKey)
			p := anthropicusage.NewPoller(client, bus, anthropicusage.PollerOptions{
				Health:      sourceHealth.For("vendor-usage-anthropic"),
				AdminKey:    cfg.VendorUsage.Anthropic.AdminKey,
				Interval:    cfg.VendorUsage.Anthropic.Interval,
				BucketWidth: anthropicusage.BucketWidth(cfg.VendorUsage.Anthropic.BucketWidth),
				Logger:      logger,
			})
			sup.Go("vendor-usage-anthropic", p.Run)
			logger.Info("anthropic admin usage poller live",
				"interval", cfg.VendorUsage.Anthropic.Interval,
				"bucket_width", cfg.VendorUsage.Anthropic.BucketWidth,
			)
		}
		if cfg.VendorUsage.GitHubCopilot.Enabled {
			p := copilotusage.NewPoller(bus, copilotusage.PollerOptions{
				Health:     sourceHealth.For("github-copilot"),
				OAuthToken: cfg.VendorUsage.GitHubCopilot.OAuthToken,
				Interval:   cfg.VendorUsage.GitHubCopilot.Interval,
				Logger:     logger,
			})
			sup.Go("github-copilot", p.Run)
			logger.Info("github copilot usage poller live",
				"interval", cfg.VendorUsage.GitHubCopilot.Interval,
			)
		}

		if cfg.Retention.Enabled() {
			policies, err := retentionPolicies(cfg.Retention)
			if err != nil {
				return fmt.Errorf("retention: %w", err)
			}
			if len(policies) > 0 {
				pruner := retention.New(components.Store, retentionConfig(cfg.Retention, policies, logger))
				retention.NewScheduler(pruner).Start(ctx)
				logger.Info("retention scheduler live",
					"policies", len(policies), "reclaim", cfg.Retention.Reclaim,
					"first_pass_in", retentionStartDelay)
			}
		}

		analyticsH, err := proxy.NewAnalyticsHandlers(components.Store, components.Aggregator, components.Spend, cfg.Coaching.WasteConfig())
		if err != nil {
			return fmt.Errorf("analytics handlers: %w", err)
		}
		opts = append(opts, proxy.WithAnalytics(analyticsH))
		opts = append(opts, proxy.WithAudit(proxy.NewAuditHandlers(components.Store)))

		// Dashboard + /api/* are protected by a shared-secret token.
		// Either the operator sets cfg.Dashboard.AdminToken via env /
		// config, or the daemon mints and persists one on first start.
		// MCP tokenops_dashboard surfaces the token so the agent hands
		// the operator a clickable URL that auto-authenticates.
		tok, errTok := loadOrMintDashToken(cfg.Dashboard.AdminToken)
		if errTok != nil {
			return fmt.Errorf("dashboard token: %w", errTok)
		}
		dashTok = tok
		auth, err := dashauth.New(dashauth.Config{
			AdminToken:   dashTok,
			SessionTTL:   24 * time.Hour,
			CookieSecure: cfg.TLS.Enabled,
		})
		if err != nil {
			return fmt.Errorf("dashboard auth: %w", err)
		}
		opts = append(opts, proxy.WithDashAuth(auth))
	}

	if cfg.Rules.Enabled {
		root := cfg.Rules.Root
		if root == "" {
			if wd, err := os.Getwd(); err == nil {
				root = wd
			} else {
				root = "."
			}
		}
		rulesH, err := proxy.NewRulesHandlers(root, cfg.Rules.RepoID)
		if err != nil {
			return fmt.Errorf("rules handlers: %w", err)
		}
		rulesH.AttachDomainBus(dbus)
		opts = append(opts, proxy.WithRules(rulesH))
		logger.Info("rule intelligence enabled", "root", root, "repo_id", cfg.Rules.RepoID)
	}

	// Declare plan coverage to the proxy so the billing basis is known at
	// request time, not just at storage time. planStampSink already
	// backfills CostSource on the way into SQLite, but the router runs in
	// the request path — well before that sink — so without this it would
	// price a flat-rate subscription at API list rates and report dollar
	// savings the operator can never realise.
	if len(cfg.Plans) > 0 {
		opts = append(opts, proxy.WithPlanCoverage(func(p eventschema.Provider) bool {
			return planCostSource(cfg, p) == eventschema.CostSourcePlanIncluded
		}))
		logger.Info("plan-covered providers declared", "count", len(cfg.Plans))
	}

	// Keep the rate card current. A model released after this binary was
	// built otherwise prices at zero, and a session that cost real money
	// reports as free — which is the failure this tool exists to find.
	if cfg.Pricing.Refresh.Enabled() && components.Spend != nil {
		go runPricingRefresh(ctx, cfg, components.Spend, logger)
		logger.Info("automatic pricing refresh on",
			"interval", cfg.Pricing.Refresh.Every(),
			"note", "fetches a public rate card; sends nothing")
	}

	// The optimizer's own mode governs what it may do with a request.
	// The daemon-wide active flag still gates the background watcher, but
	// routing no longer needs it: an operator can leave the daemon in its
	// default mode and still have the optimizer propose or observe.
	if rc := cfg.Optimizer.RouterConfig(); rc != nil {
		// Window pressure is read per request, so it comes from a cache a
		// background loop refreshes — scanning the event store inline
		// would put a full window query on the hot path.
		if components.Store != nil && len(cfg.Plans) > 0 {
			probe := newWindowProbe()
			go runWindowProbe(ctx, probe, cfg, planStoreReader{store: components.Store}, time.Minute)
			rc.WindowPressure = probe.Pct
			logger.Info("window-pressure routing available", "providers", len(cfg.Plans))
		}
		// Proposals need somewhere to live. Both the preferred-model
		// ceiling and in_request mode refer decisions to the operator, so
		// either one requires the approval log — wiring it only for the
		// ceiling would leave in_request proposing into the void.
		if len(cfg.PreferredModels) > 0 || cfg.Optimizer.Mode.Proposes() {
			if store, err := openRoutingApprovals(); err != nil {
				logger.Warn("routing approvals unavailable; routes will not be referred", "err", err)
			} else {
				attachApprovalGate(rc, cfg, store, logger)
				logger.Info("routing decisions referable",
					"preferred_models", len(cfg.PreferredModels),
					"mode", string(cfg.Optimizer.Mode))
			}
		}
		opts = append(opts, proxy.WithActiveRouting(*rc, components.Spend))
		if components.Store != nil {
			opts = append(opts, proxy.WithExperiments(experiments.New(components.Store)))
		}
		logger.Info("model routing wired",
			"rules", len(rc.Rules), "mode", string(cfg.Optimizer.Mode))
	}

	srv := proxy.New(cfg.Listen, opts...)
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}

	// The read guard prevents re-reads inside the client, where the proxy
	// cannot see them. Ingesting its ledger is what lets those savings
	// reach TEU — otherwise a client that never proxies scores "not
	// measured" however much the guard actually reclaims.
	if bus != nil {
		go runReadGuardIngest(ctx, bus, logger, 2*time.Minute, "")
		logger.Info("read-guard reclamation ingest live")
	}

	// Active-mode spend watcher: periodic budget + unpriced-model
	// evaluation against the local store. Requires storage (no events,
	// nothing to watch).
	if cfg.ActiveMode() && components.Aggregator != nil {
		go runSpendWatcher(ctx, cfg, components.Aggregator, components.Spend, logger)
	}
	// Try to advertise the daemon as tokenops.local over mDNS so the
	// dashboard URL stays memorable. Best-effort: container hosts,
	// firewalled networks, and CI runners frequently lack a usable
	// multicast interface — we log + fall back to the loopback URL
	// instead of failing the boot.
	var (
		mdnsClose      = func() {}
		mdnsPublicURL  string
		mdnsAdvertised bool
	)
	mdnsPlan := mdnsDecision(cfg.MDNS, srv.Addr())
	switch {
	case !mdnsPlan.Advertise:
		logger.Info("mdns advertise skipped", "reason", mdnsPlan.Reason)
	default:
		if closer, publicURL, err := startMDNSAdvertise(srv.Addr(), srv.TLSEnabled(), mdnsPlan.InstanceName); err != nil {
			logger.Info("mdns advertise unavailable; using loopback URL", "err", err)
		} else {
			mdnsClose = closer
			mdnsPublicURL = publicURL
			mdnsAdvertised = true
			logger.Info("mdns advertise live", "url", publicURL)
		}
	}
	defer mdnsClose()
	// Publish the listen URL so the MCP `serve` process can return a
	// clickable dashboard link via tokenops_dashboard. Removed on
	// shutdown so a stale URL never survives the daemon. Failure here
	// is non-fatal: the daemon stays up; the MCP tool just falls
	// back to "run tokenops start" guidance.
	if hintPath, err := writeURLHint(srv.Addr(), srv.TLSEnabled(), mdnsPublicURL, dashTok); err != nil {
		logger.Warn("could not publish daemon URL hint", "err", err)
	} else {
		logger.Info("daemon URL hint published",
			"path", hintPath,
			"mdns_advertised", mdnsAdvertised,
		)
		defer func() {
			if err := removeURLHint(); err != nil {
				logger.Warn("could not remove daemon URL hint", "err", err)
			}
		}()
	}
	// Publish blockers + remediation hints so /readyz exposes the same
	// signal the MCP tokenops_status tool surfaces. Operators on a fresh
	// install (storage/rules/providers off) see exactly what to fix
	// without grepping config.
	blockers := cfg.Blockers()
	proxy.SetReadyState(blockers, config.NextActionsFor(blockers))
	if len(blockers) > 0 {
		logger.Info("daemon started with blockers", "blockers", blockers)
	}
	proxy.MarkReady(true)

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Shutdown.Timeout+time.Second)
	defer cancel()
	// 1. Stop accepting new requests so no fresh domain events fire.
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("shutdown: %w", err)
	}
	// 2. Stop the pollers and wait for them, so no subsystem is still
	// writing when the buses below are drained. The bound is the
	// configured shutdown timeout: one poller that ignores cancellation
	// is a bug in that poller, not a reason for the daemon never to
	// exit.
	if err := sup.Wait(cfg.Shutdown.Timeout); err != nil {
		logger.Warn("subsystem shutdown", "err", err, "running", sup.Running())
	}
	// 3. Drain the domain bus first so its canonical-envelope bridge can
	// enqueue every final domain event before the storage bus closes.
	if !dbus.CloseWithTimeout(cfg.Shutdown.Timeout) {
		logger.Warn("domain bus drain timed out", "timeout", cfg.Shutdown.Timeout)
	}
	// 4. Drain in-flight telemetry envelopes, including the domain bridge.
	if bus != nil {
		if err := bus.Close(cfg.Shutdown.Timeout); err != nil {
			logger.Warn("event bus drain", "err", err)
		}
		logger.Info("event bus drained",
			"published", bus.PublishedCount(),
			"dropped", bus.DroppedCount(),
		)
	}
	// 5. Close the legacy log after both buses drain during the migration.
	if domainLog != nil {
		_ = domainLog.Close()
	}
	if components != nil {
		_ = components.Shutdown()
	}
	logger.Info("tokenops daemon stopped")
	return nil
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM. Callers must
// invoke the returned stop function to release signal resources.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
}

// resolveCertDir returns the cert directory to use, creating an absolute
// path. Empty input falls back to ~/.tokenops/certs so the daemon has a
// stable home without forcing every operator to set the path explicitly.
func resolveCertDir(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "certs"), nil
}

// resolveStoragePath returns the sqlite events DB path. Defaults to
// ~/.tokenops/events.db. The parent directory is created so sqlite.Open
// has a writable home.
func resolveStoragePath(configured string) (string, error) {
	path := configured
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".tokenops", "events.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// planCostSource returns the CostSource vendor-usage pollers stamp on
// emitted events: plan_included when the operator bound a flat-rate
// plan to the provider (config plans:), metered (empty) otherwise.
// Without the stamp, the analytics recompute would price
// subscription-covered usage at API list rates and budget alerts would
// fire on spend that never billed.
func planCostSource(cfg config.Config, provider eventschema.Provider) eventschema.CostSource {
	if cfg.Plans[string(provider)] != "" {
		return eventschema.CostSourcePlanIncluded
	}
	return ""
}

// browserSessionSource re-reads the claude.ai session and Cloudflare
// clearance from the operator's browser while the daemon runs, when they
// asked for that. Without it the meter is refused as soon as the clearance
// cookie expires — hours, not weeks — and the failure looks like a dead
// poller rather than an expired cookie.
func browserSessionSource(cfg config.ClaudeUsageMeterConfig) func(context.Context) (claudeusagemeter.Session, error) {
	if !cfg.FromBrowser {
		return nil
	}
	return func(ctx context.Context) (claudeusagemeter.Session, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return claudeusagemeter.Session{}, err
		}
		cookies, browser, err := browsercookie.FindMany(ctx, home, "claude.ai",
			[]string{"sessionKey", "cf_clearance"}, cfg.Browser, nil)
		if err != nil {
			return claudeusagemeter.Session{}, err
		}
		return claudeusagemeter.Session{
			Key:       cookies["sessionKey"],
			Clearance: cookies["cf_clearance"],
			UserAgent: browser.UserAgent(),
			Browser:   browser.Name,
		}, nil
	}
}
