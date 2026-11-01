package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/checkup"
	"go.klarlabs.de/tokenops/internal/capability/planoverlay"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
)

// The overlay of observed plan limits reads 90 days of window readings,
// so it is kept in the background: a while after start and once a day,
// like the week's waste report. Tier ratios move over weeks.
const (
	observedDelay = 20 * time.Minute
	observedEvery = 24 * time.Hour
)

func startObservedRuntime(cfg config.Config, c *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if c == nil || c.Store == nil || c.Spend == nil || len(cfg.Plans) == 0 {
		return
	}
	options := func(now time.Time) checkup.Options {
		o := checkup.Options{Now: now, Events: c.Store, Plans: cfg.Plans, Price: c.Spend.ComputeAt}
		if h, err := planhistory.LoadDefault(); err == nil {
			o.PlanHistory = h
		}
		return o
	}
	sup.Go("observed-limits", func(ctx context.Context) error {
		runObserved(ctx, options, observedDelay, observedEvery, logger)
		return nil
	})
}

// runObserved keeps the overlay from the tier comparisons on a schedule.
func runObserved(ctx context.Context, options func(time.Time) checkup.Options,
	delay, every time.Duration, logger *slog.Logger) {
	wait := time.NewTimer(delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	run := func() {
		now := time.Now()
		drifts := checkup.ProviderDrifts(ctx, options(now))
		if drifts == nil {
			return
		}
		v, err := planoverlay.Keep(drifts, now)
		if err != nil {
			logger.Warn("observed limits not kept", "err", err)
			return
		}
		logger.Info("observed limits kept", "limits", len(v.Limits), "off", v.Off)
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
