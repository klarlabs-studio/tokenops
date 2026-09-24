package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/domainmigration"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/rulesfs"
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
	rt.Bus = events.NewAsync(newPlanStampSink(events.NewMultiSink(sinks...), cfg), events.Options{Logger: logger})
	rt.detach = wireCanonicalObservers(rt.Bus, counter, logger)
	rt.AuditSubscriber = audit.Subscribe(rt.Bus, audit.NewRecorder(rt.Store), logger, "daemon")
	logger.Info("event store ready", "path", path)
	rt.ProxyOptions = append(rt.ProxyOptions,
		proxy.WithEventBus(rt.Bus),
		proxy.WithSourceFreshness(sourceFreshnessFn(cfg, rt.Store, sourceHealth, sup)),
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
