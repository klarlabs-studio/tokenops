package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
)

// routeObserveEvery is how often the daemon re-reads where Claude Code is
// pointed. A switch shows up within this long; the turns in between are
// read with the route that was in force when they ran, once recorded.
const routeObserveEvery = time.Minute

// startRouteHistory opens the route history, records where Claude Code is
// pointed now, and keeps watching (ADR 0009). A history that cannot be
// opened returns nil: turns are then judged by the current setting, as
// before there was a history.
func startRouteHistory(sup *lifecycle.Supervisor, logger *slog.Logger) *routehistory.Tracker {
	routes, err := routehistory.Open()
	if err != nil {
		logger.Warn("route history unavailable; judging turns by the current endpoint", "err", err)
		return nil
	}
	observe := func() {
		base := claudesettings.BaseURL()
		if wrote, err := routes.Observe(routehistory.HarnessClaudeCode, base, time.Now().UTC()); err != nil {
			logger.Warn("route history not written", "err", err)
		} else if wrote {
			logger.Info("claude code endpoint recorded", "base_url", base)
		}
	}
	observe()
	sup.Go("route-history", func(ctx context.Context) error {
		tick := time.NewTicker(routeObserveEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
				observe()
			}
		}
	})
	return routes
}

// claudeCodeBaseURLAt answers where Claude Code was pointed at a moment.
func claudeCodeBaseURLAt(routes *routehistory.Tracker) func(time.Time) string {
	if routes == nil {
		return func(time.Time) string { return claudesettings.BaseURL() }
	}
	return func(t time.Time) string { return routes.At(routehistory.HarnessClaudeCode, t) }
}
