package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/compactlever"
	"go.klarlabs.de/tokenops/internal/infra/domainmigration"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/rulesfs"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
	"go.klarlabs.de/tokenops/internal/otlp"
	"go.klarlabs.de/tokenops/internal/proxy"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// eventRuntime owns the canonical event bus and the adapters subscribed to it.
// Store lifetime remains owned by bootstrap.Components.
type eventRuntime struct {
	Store           *sqlite.Store
	Bus             *events.AsyncBus
	AuditSubscriber *audit.Subscriber
	ProxyOptions    []proxy.Option
	detach          func()
}

// initializeEventRuntime opens persistence (when enabled), migrates legacy
// records, and wires canonical observers before the proxy or pollers start.
func initializeEventRuntime(
	ctx context.Context,
	cfg config.Config,
	components *bootstrap.Components,
	counter *observ.EventCounter,
	legacyPath string,
	sourceHealth *freshness.Registry,
	sup *lifecycle.Supervisor,
	logger *slog.Logger,
) (*eventRuntime, error) {
	rt := &eventRuntime{}
	if !cfg.Storage.Enabled {
		rt.Bus = events.NewAsync(events.NoopSink{}, events.Options{Logger: logger})
		rt.detach = wireCanonicalObservers(rt.Bus, counter, logger)
		return rt, nil
	}

	path, err := resolveStoragePath(cfg.Storage.Path)
	if err != nil {
		return nil, fmt.Errorf("storage path: %w", err)
	}
	if err := components.OpenStoreAt(ctx, path); err != nil {
		return nil, err
	}
	rt.Store = components.Store
	if legacyPath != "" {
		migrated, err := domainmigration.Import(ctx, rt.Store, legacyPath, 3)
		if err != nil {
			logger.Warn("legacy domain event import incomplete; JSONL retained", "err", err)
		} else {
			logger.Info("legacy domain events imported", "read", migrated.Read, "imported", migrated.Imported, "duplicates", migrated.Duplicates, "skipped", migrated.Skipped)
		}
	}
	if history, err := rt.Store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeDomain, Limit: 1_000_000}); err != nil {
		logger.Warn("canonical domain event counter hydration failed", "err", err)
	} else {
		counter.Hydrate(history)
	}

	sinks := []events.Sink{rt.Store}
	if cfg.OTel.Enabled {
		expOpts := otlp.Options{
			Endpoint: cfg.OTel.Endpoint, Headers: cfg.OTel.Headers,
			ServiceName: cfg.OTel.ServiceName, ServiceVersion: cfg.OTel.ServiceVersion,
			Logger: logger,
		}
		if cfg.OTel.RedactEnabled() {
			expOpts.Redactor = components.Redactor
		}
		exporter, err := otlp.New(expOpts)
		if err != nil {
			_ = components.Shutdown()
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
		sinks = append(sinks, exporter)
		logger.Info("otlp exporter ready", "endpoint", cfg.OTel.Endpoint, "redact", cfg.OTel.RedactEnabled())
	}

	// Plan stamping ensures all sources inherit the plan_included contract.
	rt.Bus = events.NewAsync(newPlanStampSink(events.NewMultiSink(sinks...), cfg), events.Options{
		Logger:    logger,
		Contended: sqlite.IsContended,
	})
	rt.detach = wireCanonicalObservers(rt.Bus, counter, logger)
	rt.AuditSubscriber = audit.Subscribe(rt.Bus, audit.NewRecorder(rt.Store), logger, "daemon")
	logger.Info("event store ready", "path", path)
	rt.ProxyOptions = append(rt.ProxyOptions,
		proxy.WithEventBus(rt.Bus),
		proxy.WithSourceFreshness(sourceFreshnessFn(cfg, rt.Store, sourceHealth, sup)),
		proxy.WithPlans(plansDeps(cfg, rt.Store, components.Spend)),
		proxy.WithActions(actionDeps(cfg, rt.Store, logger)),
		proxy.WithSessions(func() proxy.SessionRoots { return proxy.SessionRoots{} }),
		proxy.WithState(stateDeps(cfg, rt.Store, sourceFreshnessFn(cfg, rt.Store, sourceHealth, sup), rt.Bus.DroppedCount)),
		proxy.WithTokenizer(components.Tokenizers),
		proxy.WithCostEngine(components.Spend),
		proxy.WithEventDrops(rt.Bus.DroppedCount),
	)
	if rt.AuditSubscriber != nil {
		rt.ProxyOptions = append(rt.ProxyOptions, proxy.WithAuditDrops(rt.AuditSubscriber.DroppedCount))
	}
	return rt, nil
}

// Detach removes process-global publisher ports after all producers stop.
func (rt *eventRuntime) Detach() {
	if rt != nil && rt.detach != nil {
		rt.detach()
		rt.detach = nil
	}
}

// Drain stops the canonical bus after publishers have stopped, then waits for
// the audit subscriber to finish consuming the accepted envelopes.
func (rt *eventRuntime) Drain(timeout time.Duration) error {
	if rt == nil {
		return nil
	}
	var drainErr error
	if rt.Bus != nil {
		drainErr = rt.Bus.Close(timeout)
	}
	if rt.AuditSubscriber != nil {
		rt.AuditSubscriber.Close()
	}
	return drainErr
}

func wireCanonicalObservers(bus *events.AsyncBus, counter *observ.EventCounter, logger *slog.Logger) func() {
	cancelPublishers := wireDomainEventPublishers(bus, logger)
	cancelCounter := counter.SubscribeCanonical(bus)
	return func() {
		cancelCounter()
		cancelPublishers()
	}
}

func wireDomainEventPublishers(bus *events.AsyncBus, logger *slog.Logger) func() {
	workflow.SetEventBus(bus)
	optimizer.SetEventBus(bus)
	rulesfs.SetEventBus(bus)
	budget.SetEventBus(bus)
	cancelLog := bus.Subscribe(func(env *eventschema.Envelope) {
		if env == nil || env.Type != eventschema.EventTypeDomain {
			return
		}
		if event, ok := env.Payload.(*eventschema.DomainEvent); ok {
			logger.Debug("domain event", "kind", event.Kind)
		}
	})
	return func() {
		cancelLog()
		workflow.SetEventBus(nil)
		optimizer.SetEventBus(nil)
		rulesfs.SetEventBus(nil)
		budget.SetEventBus(nil)
	}
}

// plansDeps is what the plan routes need. The daemon reads config once, at
// boot, and a config write restarts it (RestartForConfig), so the snapshot
// it was started with is current.
func plansDeps(cfg config.Config, store *sqlite.Store, engine *spend.Engine) func() headroom.Deps {
	return func() headroom.Deps {
		deps := headroom.Deps{Config: &cfg}
		if store != nil {
			deps.Reader = store
		}
		if engine != nil {
			deps.Price = engine.ComputeAt
		}
		return deps
	}
}

// stateDeps is what the state routes need: the daemon's own config,
// readiness, store, readers' health and lost writes.
func stateDeps(cfg config.Config, store *sqlite.Store, health func() []freshness.Report, dropped func() int64) func() state.Deps {
	return func() state.Deps {
		d := state.Deps{
			Config:  &cfg,
			Health:  health,
			Ready:   proxy.IsReady,
			Dropped: dropped,
			Coach: func(now time.Time) coachcap.Report {
				ledger, levers := coachEnv(cfg)
				return coachcap.Status(cfg, ledger, levers, now)
			},
		}
		if store != nil {
			d.Count = store.CountBySource
			d.Stale = func(ctx context.Context, now time.Time) []config.StaleSource {
				stale, err := cfg.CheckStaleIngestion(ctx, store, sourceprobe.All(cfg), config.StaleIngestionWindow, now)
				if err != nil {
					return nil
				}
				return stale
			}
		}
		return d
	}
}

// actionDeps is what the write routes need. A write lands in the file the
// daemon was started with; the daemon then restarts itself to read it,
// after the answer has been sent, when a supervisor will bring it back.
func actionDeps(cfg config.Config, store *sqlite.Store, logger *slog.Logger) func() proxy.ActionDeps {
	path := cfg.SourcePath
	if path == "" {
		if p, err := config.DefaultPath(); err == nil {
			path = p
		}
	}
	return func() proxy.ActionDeps {
		d := proxy.ActionDeps{
			ConfigPath: path,
			Apply: func() (string, func()) {
				if !UnitInstalled() {
					return ConfigRestart{}.Note(), nil
				}
				return "restarting the daemon; the change is live in a few seconds", func() {
					// Let the answer reach the client before the
					// supervisor stops this process.
					time.Sleep(250 * time.Millisecond)
					if r := RestartForConfig(); r.Err != nil {
						logger.Warn("restart after a change through the API failed", "err", r.Err)
					}
				}
			},
		}
		d.Coach = func() (coachcap.Ledger, coachcap.ContextLevers) { return coachEnv(cfg) }
		if store != nil {
			d.Store = store
			d.Audit = func(ctx context.Context, target string, details map[string]any) {
				if _, err := audit.NewRecorder(store).Record(ctx, audit.Entry{
					Action: audit.ActionConfigChange, Actor: "api", Target: target, Details: details,
				}); err != nil {
					logger.Warn("audit record for an API change failed", "err", err)
				}
			}
		}
		return d
	}
}

// coachEnv is the follow-through ledger and the context levers the coach
// reports against; either is nil when it cannot be opened.
func coachEnv(cfg config.Config) (coachcap.Ledger, coachcap.ContextLevers) {
	var ledger coachcap.Ledger
	if l, err := followthrough.Default(); err == nil {
		ledger = l
	}
	var levers coachcap.ContextLevers
	if l, err := compactlever.New(cfg); err == nil {
		levers = l
	}
	return ledger, levers
}
