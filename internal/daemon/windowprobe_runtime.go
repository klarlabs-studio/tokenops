package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// startWindowPressureRuntime installs a cached pressure reader on the routing
// config and supervises its refresh loop when storage-backed plans are usable.
func startWindowPressureRuntime(cfg config.Config, rc *router.Config, store *sqlite.Store, sup *lifecycle.Supervisor, logger *slog.Logger) *windowProbe {
	if rc == nil || store == nil || len(cfg.Plans) == 0 {
		return nil
	}
	probe := newWindowProbe()
	sup.Go("window-pressure-probe", func(taskCtx context.Context) error {
		runWindowProbe(taskCtx, probe, cfg, planStoreReader{store: store}, time.Minute)
		return nil
	})
	rc.WindowPressure = probe.Pct
	logger.Info("window-pressure routing available", "providers", len(cfg.Plans))
	return probe
}
