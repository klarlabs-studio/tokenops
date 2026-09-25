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
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
	"go.klarlabs.de/tokenops/internal/contexts/security/dashauth"
	"go.klarlabs.de/tokenops/internal/contexts/security/tlsmint"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/proxy"
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

	// Composition root constructs the counter + redactor (and other
	// long-lived collaborators) once. The canonical event bus is wired
	// once storage mode is known below.
	earlyComponents, err := bootstrap.New(ctx, bootstrap.Options{
		Logger:      logger,
		OpenStore:   false,
		PricingPath: cfg.Pricing.Path,
	})
	if err != nil {
		return err
	}
	domainEventCounter := earlyComponents.EventCounter

	// Keep the former JSONL location only as a migration input. New domain
	// events are persisted through the canonical envelope bus below.
	var domainLogPath string
	if cfg.Storage.Enabled {
		eventsPath, err := resolveStoragePath(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("storage path: %w", err)
		}
		domainLogPath = filepath.Join(filepath.Dir(eventsPath), "domain-events.jsonl")
		logger.Info("legacy domain event import path ready", "path", domainLogPath)
	}

	routes, err := proxy.BuildProviderRoutes(cfg.Providers)
	if err != nil {
		return fmt.Errorf("provider routes: %w", err)
	}

	// Every reader reports its own success and failure here, so a status
	// surface can tell a poller being refused apart from a vendor nobody
	// is using. Both produce no events; only the poll record separates
	// them.
	sourceHealth := freshness.NewRegistry()

	// Long-running subsystems run under one supervisor so shutdown waits
	// for workers to drain and failures remain attributable by task name.
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

	components := earlyComponents
	eventRuntime, err := initializeEventRuntime(ctx, cfg, components, domainEventCounter, domainLogPath, sourceHealth, sup, logger)
	if err != nil {
		return err
	}
	defer eventRuntime.Detach()
	bus := eventRuntime.Bus
	dashTok := ""
	opts = append(opts, eventRuntime.ProxyOptions...)

	if cfg.Storage.Enabled {
		// Source-specific polling configuration lives in its runtime module.
		startVendorUsagePollers(cfg, bus, sourceHealth, sup, logger)

		if err := startRetentionRuntime(cfg.Retention, components.Store, sup, logger); err != nil {
			return fmt.Errorf("retention: %w", err)
		}

		analyticsH, err := proxy.NewAnalyticsHandlers(components.Store, components.Aggregator, components.Spend, cfg.Coaching.WasteConfig())
		if err != nil {
			return fmt.Errorf("analytics handlers: %w", err)
		}
		opts = append(opts, proxy.WithAnalytics(analyticsH))
		opts = append(opts, proxy.WithAudit(proxy.NewAuditHandlers(components.Store)))

		// The local API is protected by a shared-secret bearer token.
		// Either the operator sets cfg.Dashboard.AdminToken via env /
		// config, or the daemon mints and persists one on first start.
		tok, errTok := loadOrMintDashToken(cfg.Dashboard.AdminToken)
		if errTok != nil {
			return fmt.Errorf("API token: %w", errTok)
		}
		dashTok = tok
		auth, err := dashauth.New(dashauth.Config{
			AdminToken: dashTok,
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
		cancelRulesObserver := rulesH.AttachEventBus(bus)
		defer cancelRulesObserver()
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
	startPricingRefreshRuntime(cfg, components.Spend, sup, logger)

	// The optimizer's own mode governs what it may do with a request.
	// The daemon-wide active flag still gates the background watcher, but
	// routing no longer needs it: an operator can leave the daemon in its
	// default mode and still have the optimizer propose or observe.
	if rc := cfg.Optimizer.RouterConfig(); rc != nil {
		// Window pressure is read per request, so it comes from a cache a
		// background loop refreshes — scanning the event store inline
		// would put a full window query on the hot path.
		startWindowPressureRuntime(cfg, rc, components.Store, sup, logger)
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
	startReadGuardRuntime(bus, sup, logger)

	// Active-mode spend watcher: periodic budget + unpriced-model
	// evaluation against the local store. Requires storage (no events,
	// nothing to watch).
	startSpendWatcherRuntime(cfg, components.Aggregator, components.Spend, sup, logger)
	defer publishRuntimeAnnouncement(cfg, srv, dashTok, logger)()
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
	// 3. Drain in-flight canonical envelopes after all publishers stop.
	if err := eventRuntime.Drain(cfg.Shutdown.Timeout); err != nil {
		logger.Warn("event bus drain", "err", err)
	}
	logger.Info("event bus drained",
		"published", bus.PublishedCount(),
		"dropped", bus.DroppedCount(),
	)
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
