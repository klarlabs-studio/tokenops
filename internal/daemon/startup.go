package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/capability/routers"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/proxy"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// startup carries the state one daemon run accumulates as its boot steps
// execute. Each step reads what earlier steps produced and adds to it;
// RunWithLogger runs them in a fixed order, so the order of side effects,
// log lines and proxy options is the order of the step list.
type startup struct {
	cfg    config.Config
	logger *slog.Logger

	components     *bootstrap.Components
	eventCounter   *bootstrap.EventCounter
	domainLogPath  string
	providerRoutes []proxy.ProviderRoute
	sourceHealth   *state.SourceRegistry
	sup            *lifecycle.Supervisor
	events         *eventRuntime
	// ingest is the bus readers replaying history publish to: the event
	// bus minus what the store already held at boot. Without storage it
	// is the event bus itself.
	ingest    events.Bus
	opts      []proxy.Option
	dashToken string
	server    *proxy.Server

	// cleanups run in reverse order when the run ends, as the deferred
	// calls they replace did.
	cleanups []func()
}

// startupStep is one stage of the boot sequence.
type startupStep func(ctx context.Context) error

// steps lists the boot stages in the order they run.
func (s *startup) steps() []startupStep {
	return []startupStep{
		s.announce,
		s.buildComponents,
		s.resolveLegacyEventLog,
		s.buildProviderRoutes,
		s.startSupervision,
		s.buildTransportOptions,
		s.configureTLS,
		s.startEventRuntime,
		s.startStorageSubsystems,
		s.configureAPIAuth,
		s.configureRules,
		s.declarePlanCoverage,
		s.startBackgroundRefreshers,
		s.configureRouting,
		s.serve,
		s.startPostServeSubsystems,
		s.announceReady,
	}
}

// deferCleanup registers fn to run when the daemon run ends.
func (s *startup) deferCleanup(fn func()) { s.cleanups = append(s.cleanups, fn) }

// runCleanups runs the registered cleanups, newest first.
func (s *startup) runCleanups() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

// announce validates the configuration and logs the start banner along
// with any warnings about the listen address.
func (s *startup) announce(context.Context) error {
	if err := s.cfg.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	s.logger.Info("tokenops daemon starting",
		"version", version.Version,
		"commit", version.Commit,
		"listen", s.cfg.Listen,
	)
	for _, w := range s.cfg.ListenWarnings() {
		s.logger.Warn(w, "listen", s.cfg.Listen, "tls", s.cfg.TLS.Enabled)
	}
	return nil
}

// buildComponents has the composition root construct the counter and
// redactor (and the other long-lived collaborators) once. The store
// stays closed here; the event runtime opens it once storage mode is
// known.
func (s *startup) buildComponents(ctx context.Context) error {
	components, err := bootstrap.New(ctx, bootstrap.Options{
		Logger:      s.logger,
		OpenStore:   false,
		PricingPath: s.cfg.Pricing.Path,
	})
	if err != nil {
		return err
	}
	s.components = components
	s.eventCounter = components.EventCounter
	return nil
}

// resolveLegacyEventLog keeps the former JSONL location only as a
// migration input. New domain events are persisted through the canonical
// envelope bus.
func (s *startup) resolveLegacyEventLog(context.Context) error {
	if !s.cfg.Storage.Enabled {
		return nil
	}
	eventsPath, err := resolveStoragePath(s.cfg.Storage.Path)
	if err != nil {
		return fmt.Errorf("storage path: %w", err)
	}
	s.domainLogPath = filepath.Join(filepath.Dir(eventsPath), "domain-events.jsonl")
	s.logger.Info("legacy domain event import path ready", "path", s.domainLogPath)
	return nil
}

// buildProviderRoutes resolves the upstream each provider is proxied to.
func (s *startup) buildProviderRoutes(context.Context) error {
	routes, err := proxy.BuildProviderRoutes(s.cfg.Providers)
	if err != nil {
		return fmt.Errorf("provider routes: %w", err)
	}
	s.providerRoutes = routes
	return nil
}

// startSupervision creates the source-health registry and the supervisor.
//
// Every reader reports its own success and failure to the registry, so a
// status surface can tell a poller being refused apart from a vendor
// nobody is using. Both produce no events; only the poll record separates
// them. Long-running subsystems run under the one supervisor so shutdown
// waits for workers to drain and failures remain attributable by task
// name.
func (s *startup) startSupervision(ctx context.Context) error {
	s.sourceHealth = state.NewSourceRegistry()
	s.sup = lifecycle.New(ctx, s.logger)
	return nil
}

// buildTransportOptions sets the proxy's base options: logging, shutdown,
// provider routes, allowed hosts, event counts, and resilience when
// enabled.
func (s *startup) buildTransportOptions(context.Context) error {
	counter := s.eventCounter
	s.opts = append(s.opts,
		proxy.WithLogger(s.logger),
		proxy.WithShutdownTimeout(s.cfg.Shutdown.Timeout),
		proxy.WithProviderRoutes(s.providerRoutes),
		proxy.WithAllowedHosts(proxyAllowedHosts(s.cfg, hostnameOrEmpty())...),
		proxy.WithEventCounts(counter.Counts),
		proxy.WithEventSpans(func() map[string]proxy.EventSpan {
			spans := counter.Spans()
			out := make(map[string]proxy.EventSpan, len(spans))
			for k, v := range spans {
				out[k] = proxy.EventSpan{First: v.First, Last: v.Last}
			}
			return out
		}),
	)
	if r := s.cfg.Resilience; r.Enabled {
		s.opts = append(s.opts, proxy.WithResilience(proxy.ResilienceConfig{
			FirstByteTimeout: r.FirstByteTimeout,
			IdleTimeout:      r.IdleTimeout,
			TotalTimeout:     r.TotalTimeout,
			FailureThreshold: r.FailureThreshold,
		}))
		s.logger.Info("resilience enabled",
			"first_byte_timeout", r.FirstByteTimeout,
			"idle_timeout", r.IdleTimeout,
			"total_timeout", r.TotalTimeout,
			"failure_threshold", r.FailureThreshold,
		)
	}
	return nil
}

// configureTLS ensures the local certificate bundle exists and serves
// over it when TLS is enabled.
func (s *startup) configureTLS(context.Context) error {
	if !s.cfg.TLS.Enabled {
		return nil
	}
	certDir, err := resolveCertDir(s.cfg.TLS.CertDir)
	if err != nil {
		return fmt.Errorf("tls cert dir: %w", err)
	}
	bundle, err := bootstrap.TLSBundle(certDir, s.cfg.TLS.Hostnames)
	if err != nil {
		return fmt.Errorf("tls bundle: %w", err)
	}
	s.logger.Info("tls bundle ready",
		"cert_dir", bundle.Dir,
		"leaf_not_after", bundle.LeafCert.NotAfter,
	)
	s.opts = append(s.opts, proxy.WithTLS(bundle.TLSConfig()))
	return nil
}

// startEventRuntime opens persistence (when enabled) and wires the
// canonical event bus and its observers. The bus is detached when the
// run ends.
func (s *startup) startEventRuntime(ctx context.Context) error {
	rt, err := initializeEventRuntime(ctx, s.cfg, s.components, s.eventCounter, s.domainLogPath, s.sourceHealth, s.sup, s.logger)
	if err != nil {
		return err
	}
	s.events = rt
	s.deferCleanup(rt.Detach)
	s.opts = append(s.opts, rt.ProxyOptions...)
	return nil
}

// startStorageSubsystems runs what needs the event store: route history,
// the attribution corrections, the vendor-usage pollers, retention, and
// the analytics and audit API.
func (s *startup) startStorageSubsystems(ctx context.Context) error {
	if !s.cfg.Storage.Enabled {
		return nil
	}
	store := s.components.Store
	routes := startRouteHistory(s.sup, s.logger)
	correctAttribution(ctx, s.cfg, store, routes, s.logger)
	correctSpendCoverage(ctx, s.cfg, store, s.logger)
	// The composition root builds and starts the vendor-usage pollers.
	s.ingest = ingestionBus(ctx, s.events.Bus, store, s.logger)
	refresh := bootstrap.StartVendorUsagePollers(s.cfg, s.ingest, s.sourceHealth, claudeCodeBaseURLAt(routes), s.sup, s.logger)
	s.opts = append(s.opts, proxy.WithSourcesRefresh(refresh))

	if err := bootstrap.StartRetentionRuntime(s.cfg.Retention, store, s.sup, s.logger); err != nil {
		return fmt.Errorf("retention: %w", err)
	}

	analyticsH, err := proxy.NewAnalyticsHandlers(store, s.components.Aggregator, s.components.Spend, s.cfg.Coaching.WasteConfig())
	if err != nil {
		return fmt.Errorf("analytics handlers: %w", err)
	}
	s.opts = append(s.opts, proxy.WithAnalytics(analyticsH))
	s.opts = append(s.opts, proxy.WithAudit(proxy.NewAuditHandlers(store)))
	return nil
}

// configureAPIAuth protects the local API with a shared-secret bearer
// token whatever else is configured: with storage disabled /api/* still
// serves the rules API, which reads rule files from any ?root= a caller
// names. Either the operator sets cfg.Dashboard.AdminToken via env /
// config, or the daemon mints and persists one on first start.
func (s *startup) configureAPIAuth(context.Context) error {
	tok, err := loadOrMintDashToken(s.cfg.Dashboard.AdminToken)
	if err != nil {
		return fmt.Errorf("API token: %w", err)
	}
	s.dashToken = tok
	auth, err := bootstrap.APIAuthenticator(tok)
	if err != nil {
		return fmt.Errorf("dashboard auth: %w", err)
	}
	s.opts = append(s.opts, proxy.WithDashAuth(proxy.BearerAuth(auth)))
	return nil
}

// configureRules serves rule intelligence when enabled, observing the
// event bus until the run ends.
func (s *startup) configureRules(context.Context) error {
	if !s.cfg.Rules.Enabled {
		return nil
	}
	root := s.cfg.Rules.Root
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		} else {
			root = "."
		}
	}
	rulesH, err := proxy.NewRulesHandlers(root, s.cfg.Rules.RepoID)
	if err != nil {
		return fmt.Errorf("rules handlers: %w", err)
	}
	s.deferCleanup(rulesH.AttachEventBus(s.events.Bus))
	s.opts = append(s.opts, proxy.WithRules(rulesH))
	s.logger.Info("rule intelligence enabled", "root", root, "repo_id", s.cfg.Rules.RepoID)
	return nil
}

// declarePlanCoverage tells the proxy which providers a plan covers, so
// the billing basis is known at request time, not just at storage time.
// planStampSink already backfills CostSource on the way into SQLite, but
// the router runs in the request path — well before that sink — so
// without this it would price a flat-rate subscription at API list rates
// and report dollar savings the operator can never realise.
func (s *startup) declarePlanCoverage(context.Context) error {
	if len(s.cfg.Plans) == 0 {
		return nil
	}
	cfg := s.cfg
	s.opts = append(s.opts, proxy.WithPlanCoverage(func(p eventschema.Provider) bool {
		return bootstrap.PlanCostSource(cfg, p) == eventschema.CostSourcePlanIncluded
	}))
	s.logger.Info("plan-covered providers declared", "count", len(s.cfg.Plans))
	return nil
}

// startBackgroundRefreshers starts the loops that keep cached inputs
// current before the server starts.
func (s *startup) startBackgroundRefreshers(context.Context) error {
	// Keep the rate card current. A model released after this binary was
	// built otherwise prices at zero, and a session that cost real money
	// reports as free — which is the failure this tool exists to find.
	startPricingRefreshRuntime(s.cfg, s.components.Spend, s.sup, s.logger)

	// The findings' session analysis is too slow to run per answer.
	startSessionFindingsRuntime(s.sup, s.logger)
	return nil
}

// configureRouting wires model routing when a router config exists.
//
// The optimizer's own mode governs what it may do with a request. The
// daemon-wide active flag still gates the background watcher, but routing
// no longer needs it: an operator can leave the daemon in its default
// mode and still have the optimizer propose or observe.
func (s *startup) configureRouting(context.Context) error {
	rc := s.cfg.RouterConfig()
	if rc == nil {
		return nil
	}
	// Window pressure is read per request, so it comes from a cache a
	// background loop refreshes — scanning the event store inline would
	// put a full window query on the hot path.
	startWindowPressureRuntime(s.cfg, rc, s.components.Store, s.sup, s.logger)
	// Proposals need somewhere to live. Both the preferred-model ceiling
	// and in_request mode refer decisions to the operator, so either one
	// requires the approval log — wiring it only for the ceiling would
	// leave in_request proposing into the void.
	if len(s.cfg.PreferredModels) > 0 || s.cfg.Optimizer.Mode.Proposes() {
		if store, err := routers.OpenApprovals(); err != nil {
			s.logger.Warn("routing approvals unavailable; routes will not be referred", "err", err)
		} else {
			routers.AttachApprovalGate(rc, s.cfg, store, s.logger)
			s.logger.Info("routing decisions referable",
				"preferred_models", len(s.cfg.PreferredModels),
				"mode", string(s.cfg.Optimizer.Mode))
		}
	}
	s.opts = append(s.opts, proxy.WithActiveRouting(*rc, s.components.Spend))
	if s.components.Store != nil {
		s.opts = append(s.opts, proxy.WithExperiments(experiments.New(s.components.Store)))
	}
	s.logger.Info("model routing wired",
		"rules", len(rc.Rules), "mode", string(s.cfg.Optimizer.Mode))
	return nil
}

// serve builds the proxy server from the accumulated options and starts
// it listening.
func (s *startup) serve(ctx context.Context) error {
	srv := proxy.New(s.cfg.Listen, s.opts...)
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	s.server = srv
	return nil
}

// startPostServeSubsystems starts the subsystems that run alongside the
// serving proxy.
func (s *startup) startPostServeSubsystems(context.Context) error {
	// The read guard prevents re-reads inside the client, where the proxy
	// cannot see them. Ingesting its ledger is what lets those savings
	// reach TEU — otherwise a client that never proxies scores "not
	// measured" however much the guard actually reclaims.
	ingest := s.ingest
	if ingest == nil {
		ingest = s.events.Bus
	}
	startReadGuardRuntime(ingest, s.sup, s.logger)

	// Active-mode spend watcher: periodic budget + unpriced-model
	// evaluation against the local store. Requires storage (no events,
	// nothing to watch).
	if s.components.Aggregator != nil {
		startSpendWatcherRuntime(s.cfg, s.components.Aggregator, s.events.Bus, s.components.Store, s.components.Spend.Currency(), s.sup, s.logger)
	}
	return nil
}

// announceReady advertises the daemon (mDNS, URL hint) and marks it
// ready.
func (s *startup) announceReady(context.Context) error {
	s.deferCleanup(publishRuntimeAnnouncement(s.cfg, s.server, s.dashToken, s.logger))
	// Publish blockers + remediation hints so /readyz exposes the same
	// signal the MCP tokenops_status tool surfaces. Operators on a fresh
	// install (storage/rules/providers off) see exactly what to fix
	// without grepping config.
	blockers := s.cfg.Blockers()
	proxy.SetReadyState(blockers, config.NextActionsFor(blockers))
	if len(blockers) > 0 {
		s.logger.Info("daemon started with blockers", "blockers", blockers)
	}
	proxy.MarkReady(true)
	return nil
}

// shutdown tears the run down through stopDaemon.
func (s *startup) shutdown() error {
	rt, bus, logger := s.events, s.events.Bus, s.logger
	return stopDaemon(logger, s.cfg.Shutdown.Timeout, shutdownSteps{
		server:         s.server,
		waitSubsystems: s.sup.Wait,
		drainEvents: func(d time.Duration) error {
			err := rt.Drain(d)
			var skipped int64
			if rt.Store != nil {
				skipped = rt.Store.SkippedInvalid()
			}
			logger.Info("event bus drained",
				"published", bus.PublishedCount(),
				"dropped", bus.DroppedCount(),
				"skipped_invalid", skipped,
			)
			return err
		},
		closeComponents: func() {
			if s.components != nil {
				_ = s.components.Shutdown()
			}
		},
		running: s.sup.Running,
	})
}
