package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
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
func startOTelMetricsRuntime(cfg config.Config, store *sqlite.Store, components *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
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
	g := &telemetry.Gatherer{Glance: plansDeps(cfg, store, eng), Agg: components.Aggregator, Coach: func(now time.Time) *coachcap.Report {
		ledger, levers := coachEnv(cfg)
		r := coachcap.Status(cfg, ledger, levers, now)
		return &r
	}}
	sup.Go("otel-metrics", func(ctx context.Context) error {
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			at := time.Now().UTC()
			if err := exp.Push(ctx, telemetry.Gauges(g.Gather(ctx, at)), at); err != nil {
				logger.Warn("otlp metrics push failed; the next push sends fresh figures", "err", err)
			}
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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
