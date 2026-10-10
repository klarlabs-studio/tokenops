package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/teamshare"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/internal/version"
)

// teamFirstUpload is how long after start the first upload waits, so it
// does not compete with startup.
const teamFirstUpload = 2 * time.Minute

// startTeamUploadRuntime uploads derived figures to the team plane this
// machine joined (ADR 0012), every team.interval. It reads the enrolment
// on every tick, so `tokenops team join` and `leave` take effect without a
// restart; until a machine joins, a tick reads one missing file and does
// nothing.
func startTeamUploadRuntime(cfg config.Config, components *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if !cfg.Team.UploadsEnabled() || components == nil {
		return
	}
	path, err := teamclient.ResolveStatePath(cfg.Team.StatePath)
	if err != nil {
		logger.Warn("team uploads not started", "err", err)
		return
	}
	every := cfg.Team.EffectiveInterval()
	opts := teamshare.Options{Days: cfg.Team.EffectiveDays(), RepoNames: cfg.Team.EffectiveRepoNames(), ClientVersion: version.Version}
	deps := teamshare.DepsFor(components.Aggregator)
	sup.Go("team-upload", func(ctx context.Context) error {
		wait := time.NewTimer(teamFirstUpload)
		defer wait.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-wait.C:
		}
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			uploadTeamOnce(ctx, deps, opts, path, logger)
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
		}
	})
}

func uploadTeamOnce(ctx context.Context, deps teamshare.Deps, opts teamshare.Options, path string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	res, err := teamshare.Sync(ctx, deps, opts, path, time.Now())
	switch {
	case errors.Is(err, teamclient.ErrNotJoined):
		return
	case errors.As(err, new(*teamshare.PausedError)):
		// Billing, not a fault: say so plainly, and keep trying hourly so
		// uploads resume on their own once a subscription starts.
		logger.Info("team uploads paused by the organisation's billing", "detail", err.Error())
	case err != nil:
		// The error names the server's refusal; it never carries the
		// device token or any figure.
		logger.Warn("team upload failed; the next one resends these days", "err", err)
	default:
		logger.Info("team upload", "rows", res.Response.Accepted, "days", len(res.Upload.Days),
			"duplicate", res.Response.Duplicate, "stale", res.Response.Stale,
			"note", "derived figures only; see tokenops team preview")
	}
	for _, w := range res.Warnings {
		logger.Warn("team upload: part of the figures could not be read", "detail", w)
	}
}
