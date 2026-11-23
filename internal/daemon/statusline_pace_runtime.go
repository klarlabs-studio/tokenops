package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/statuslinepace"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/version"
)

// startStatuslinePaceRuntime keeps the status line's pace forecast fresh
// whether or not a client asks for the glance: it writes the forecast now
// and every statuslinepace.Every, which is inside the freshness bound the
// status line applies. The forecast is the plan headroom glance reads; it
// is computed from events here, not from the glance's snapshot.
func startStatuslinePaceRuntime(glance func() headroom.Deps, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if sup == nil {
		return
	}
	k := statuslinePaceKeeper(glance, statuslinepace.DefaultPath(), func() time.Time { return time.Now().UTC() },
		func(err error) {
			logger.Warn("status line forecast not written; the status line leaves its forecast out", "err", err)
		})
	sup.Go("statusline-pace", func(ctx context.Context) error {
		tick := time.NewTicker(statuslinepace.Every)
		defer tick.Stop()
		k.Run(ctx, tick.C)
		return nil
	})
}

// statuslinePaceKeeper is the writer for path, reading the plan headroom
// through glance for the clock now.
func statuslinePaceKeeper(glance func() headroom.Deps, path string, now func() time.Time, warn func(error)) statuslinepace.Keeper {
	return statuslinepace.Keeper{
		Path:    path,
		Version: version.Version,
		Now:     now,
		Warn:    warn,
		Source: func(ctx context.Context, at time.Time) ([]headroom.Report, error) {
			res, err := headroom.Compute(ctx, glance(), at)
			return res.Reports, err
		},
	}
}
