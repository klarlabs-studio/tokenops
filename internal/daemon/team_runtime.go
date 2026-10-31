package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/teamshare"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/internal/version"
)

// teamFirstUpload is how long after start the first upload waits, so it
// does not compete with startup.
const teamFirstUpload = 2 * time.Minute

// teamUploadTimeout bounds one upload, reading and sending, so a hung one
// does not hold the next. macOS runs the daemon at background priority
// (ProcessType Background), where an upload's reads take about four times
// as long as in a terminal: two minutes an hour on a machine with 2,300
// sessions, over four after a start. Five minutes cut uploads off; the
// log line says how long each part took, so one that runs out names it.
const teamUploadTimeout = 15 * time.Minute

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
	opts := teamshare.Options{
		Days: cfg.Team.EffectiveDays(), RepoNames: cfg.Team.EffectiveRepoNames(), ClientVersion: version.Version,
		Allow: shareAllowed(cfg, bcap.DestinationTeam),
	}
	// The schema 2 and 3 sections are read only when the server accepts
	// them (teamshare.ServerSchema); vendor cost only on an owner's or
	// admin's machine, with the admin keys configured here.
	deps := teamshare.MachineDeps(cfg, components.Aggregator, components.Store, components.Spend,
		func(ctx context.Context) map[string]string { return bootstrap.AdminKeys(ctx, cfg) })
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

// shareAllowed is the share boundary for destination, asked before
// figures leave. Nil when no rule concerns sharing.
func shareAllowed(cfg config.Config, destination string) func() error {
	rules := bcap.LiveRules(cfg)
	return func() error { return bcap.CheckShare(rules(), destination, bcap.Env{Approval: cfg.ApprovalMode()}) }
}

func uploadTeamOnce(ctx context.Context, deps teamshare.Deps, opts teamshare.Options, path string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, teamUploadTimeout)
	defer cancel()
	start := time.Now()
	parts := []any{}
	opts.Took = func(part string, took time.Duration) {
		parts = append(parts, slog.Duration(part, took.Round(time.Millisecond)))
	}
	res, err := teamshare.Sync(ctx, deps, opts, path, start)
	took := slog.Group("took", append([]any{slog.Duration("total", time.Since(start).Round(time.Millisecond))}, parts...)...)
	switch {
	case errors.Is(err, teamclient.ErrNotJoined):
		return
	case bcap.IsShareRefused(err):
		logger.Info("team upload held by a boundary", "detail", err.Error())
	case errors.As(err, new(*teamshare.PausedError)):
		// Billing, not a fault: say so plainly, and keep trying hourly so
		// uploads resume on their own once a subscription starts.
		logger.Info("team uploads paused by the organisation's billing", "detail", err.Error())
	case err != nil:
		// The error names the server's refusal; it never carries the
		// device token or any figure.
		logger.Warn("team upload failed; the next one resends these days", "err", err, took)
	default:
		logger.Info("team upload", "rows", res.Response.Accepted, "days", len(res.Upload.Days),
			"duplicate", res.Response.Duplicate, "stale", res.Response.Stale, took,
			"note", "derived figures only; see tokenops team preview")
	}
	for _, w := range res.Warnings {
		logger.Warn("team upload: part of the figures could not be read", "detail", w)
	}
}
