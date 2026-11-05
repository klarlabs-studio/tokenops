package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/routers"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// startWindowPressureRuntime installs a cached pressure reader on the routing
// config and supervises its refresh loop when storage-backed plans are usable.
func startWindowPressureRuntime(cfg config.Config, rc *routers.Config, events headroom.EventReader, sup *lifecycle.Supervisor, logger *slog.Logger) *windowProbe {
	if rc == nil || events == nil || len(cfg.Plans) == 0 {
		return nil
	}
	probe := newWindowProbe()
	sup.Go("window-pressure-probe", func(taskCtx context.Context) error {
		runWindowProbe(taskCtx, probe, cfg, events, time.Minute)
		return nil
	})
	rc.WindowPressure = probe.Pct
	logger.Info("window-pressure routing available", "providers", len(cfg.Plans))
	return probe
}
