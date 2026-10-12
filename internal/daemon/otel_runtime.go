package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/telemetry"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/otlp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/version"
)

// startOTelMetricsRuntime pushes TokenOps' derived metrics to the
// configured collector every interval: the figures the glance, the coach
// and cost per commit show, and nothing they are derived from.
func startOTelMetricsRuntime(cfg config.Config, store *sqlite.Store, glance *sqlite.EventCache, components *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
	eng := components.Spend
	if !cfg.OTel.MetricsEnabled() || store == nil || eng == nil || components.Aggregator == nil {
		return
	}
	exp, err := otlp.NewMetrics(otlp.Options{
		Endpoint: cfg.OTel.Endpoint, Headers: cfg.OTel.Headers,
		ServiceName: cfg.OTel.ServiceName, ServiceVersion: firstNonEmpty(cfg.OTel.ServiceVersion, version.Version),
		Logger: logger,
	})
	if err != nil {
		logger.Warn("otlp metrics not started", "err", err)
		return
	}
	every := cfg.OTel.Interval
	if every <= 0 {
		every = telemetry.Every
	}
	g := &telemetry.Gatherer{Glance: plansDeps(cfg, glance, eng), Agg: components.Aggregator, Coach: func(now time.Time) *coachcap.Report {
		ledger, levers := coachEnv(cfg)
		r := coachcap.Status(cfg, ledger, levers, now)
		return &r
	}}
	allow := shareAllowed(cfg, bcap.DestinationOTel)
	sup.Go("otel-metrics", func(ctx context.Context) error {
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			pushMetricsOnce(ctx, exp, g, allow, logger)
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
		}
	})
	logger.Info("otlp metrics on", "endpoint", cfg.OTel.Endpoint, "interval", every,
		"note", "derived figures only; no events, prompts or files")
}

// pushMetricsOnce sends one set of gauges, unless a share boundary holds
// them.
func pushMetricsOnce(ctx context.Context, exp *otlp.MetricsExporter, g *telemetry.Gatherer, allow func() error, logger *slog.Logger) {
	if allow != nil {
		if err := allow(); err != nil {
			logger.Info("otlp metrics push held by a boundary", "detail", err.Error())
			return
		}
	}
	at := time.Now().UTC()
	if err := exp.Push(ctx, telemetry.Gauges(g.Gather(ctx, at)), at); err != nil {
		logger.Warn("otlp metrics push failed; the next push sends fresh figures", "err", err)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
