package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/anomalies"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// Spend anomaly detection reads four weeks of turns, so it runs in the
// background like the session analysis: a few minutes after start, once
// ingestion has caught up, and every hour after, so a day running hot is
// seen the same day. The findings read the stored answer.
const (
	anomalyDelay = 6 * time.Minute
	anomalyEvery = time.Hour
)

func startAnomalyRuntime(cfg config.Config, c *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if c == nil || c.Aggregator == nil {
		return
	}
	d := anomalies.DepsFor(cfg, c.Aggregator, c.Store, c.Spend)
	sup.Go("spend-anomalies", func(ctx context.Context) error {
		runAnomalies(ctx, d, findings.DefaultDir(), anomalyDelay, anomalyEvery, logger)
		return nil
	})
}

func runAnomalies(ctx context.Context, d anomalies.Deps, dir string, delay, every time.Duration, logger *slog.Logger) {
	wait := time.NewTimer(delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	run := func() {
		start := time.Now()
		s, err := anomalies.Run(ctx, d, dir, start)
		if err != nil {
			logger.Warn("spend anomalies not stored", "err", err)
			return
		}
		logger.Info("spend anomalies stored", "took", time.Since(start).Round(time.Millisecond),
			"anomalies", len(s.Anomalies), "history_days", s.HistoryDays)
	}
	run()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}
