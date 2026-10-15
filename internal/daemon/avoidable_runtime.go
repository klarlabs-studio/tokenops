package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// The week's waste report reads five weeks of turns, every workflow of
// the week and git history, so it runs in the background: a few minutes
// after start, once ingestion has caught up, and once a day after. The
// glance's weekly headline reads the stored answer.
const (
	avoidableDelay = 10 * time.Minute
	avoidableEvery = 24 * time.Hour
)

func startAvoidableRuntime(cfg config.Config, c *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if c == nil || c.Aggregator == nil {
		return
	}
	d := avoidable.DepsFor(cfg, c.Aggregator, c.Store, c.Spend)
	sup.Go("avoidable-week", func(ctx context.Context) error {
		runAvoidable(ctx, d, findings.DefaultDir(), avoidableDelay, avoidableEvery, logger)
		return nil
	})
}

func runAvoidable(ctx context.Context, d avoidable.Deps, dir string, delay, every time.Duration, logger *slog.Logger) {
	wait := time.NewTimer(delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	run := func() {
		start := time.Now()
		s, err := avoidable.Run(ctx, d, dir, start)
		if err != nil {
			logger.Warn("waste report not stored", "err", err)
			return
		}
		logger.Info("waste report stored", "took", time.Since(start).Round(time.Millisecond),
			"avoidable_tokens", s.Report.Avoidable.Tokens)
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
