package daemon

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// startSpendWatcherRuntime owns registration of the active-mode budget watcher.
func startSpendWatcherRuntime(cfg config.Config, agg *analytics.Aggregator, spendEngine *spend.Engine, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if !cfg.ActiveMode() || agg == nil {
		return
	}
	sup.Go("spend-watcher", func(taskCtx context.Context) error {
		runSpendWatcher(taskCtx, cfg, agg, spendEngine, logger)
		return nil
	})
}
