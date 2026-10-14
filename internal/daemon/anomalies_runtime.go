package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/anomalies"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/workflowtrace"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
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
	d := anomalyDeps(cfg, c)
	sup.Go("spend-anomalies", func(ctx context.Context) error {
		runAnomalies(ctx, d, findings.DefaultDir(), anomalyDelay, anomalyEvery, logger)
		return nil
	})
}

// anomalyDeps wires the run to the event store, the clients' session
// directories, the waste detector and the follow-through ledger.
func anomalyDeps(cfg config.Config, c *bootstrap.Components) anomalies.Deps {
	agg := c.Aggregator
	planned := map[string]bool{}
	for p := range cfg.Plans {
		if cfg.PlanCovers(p) {
			planned[p] = true
		}
	}
	d := anomalies.Deps{
		Turns: func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
			return agg.SessionTurns(ctx, analytics.Filter{Since: since})
		},
		Charges: func(ctx context.Context, since, until time.Time) ([]analytics.Charge, error) {
			return agg.DailyCharges(ctx, analytics.Filter{Since: since, Until: until})
		},
		RepoOf:        anomalies.SessionRepos(gitlog.NewRepos()),
		PlanProviders: planned,
	}
	if c.Store != nil {
		store, eng, wcfg := c.Store, c.Spend, cfg.Coaching.WasteConfig()
		d.Waste = func(ctx context.Context, wf string) []string {
			det, err := workflowtrace.Find(ctx, store, eng, wf, wcfg)
			if err != nil {
				return nil
			}
			var out []string
			for _, f := range det.Findings {
				out = append(out, f.Summary)
			}
			return out
		}
	}
	if l, err := followthrough.Default(); err == nil {
		d.Ledger = l
	}
	return d
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
