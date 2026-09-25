package daemon

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// startPricingRefreshRuntime owns registration and operator-facing status for
// the recurring pricing refresh loop.
func startPricingRefreshRuntime(cfg config.Config, engine *spend.Engine, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if !cfg.Pricing.Refresh.Enabled() || engine == nil {
		return
	}
	sup.Go("pricing-refresh", func(taskCtx context.Context) error {
		runPricingRefresh(taskCtx, cfg, engine, logger)
		return nil
	})
	logger.Info("automatic pricing refresh on",
		"interval", cfg.Pricing.Refresh.Every(),
		"note", "fetches a public rate card; sends nothing")
}
