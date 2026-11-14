package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/teamshare"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
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
func startTeamUploadRuntime(cfg config.Config, components *bootstrap.Components, plans func() headroom.Deps, transcripts *sessions.TranscriptCache,
	waste func(*avoidable.Deps), sup *lifecycle.Supervisor, logger *slog.Logger) {
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
	var adjust []func(*avoidable.Deps)
	if waste != nil {
		adjust = append(adjust, waste)
	}
	deps := teamshare.MachineDeps(cfg, components.Aggregator, components.Store, components.Spend,
		func(ctx context.Context) map[string]string { return bootstrap.AdminKeys(ctx, cfg) }, adjust...)
	if plans != nil && deps.FinOps.Plans != nil {
		deps.FinOps.Plans = plans
	}
	if transcripts != nil {
		deps.Units = teamshare.UnitsFrom(transcripts)
	}
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
		var cadence teamCadence
		full := opts.Days
		for {
			now := time.Now()
			o := opts
			o.Days, o.FullDays = cadence.days(now, full), full
			// Read before the upload, so events that arrive while it is
			// built make the next one run.
			pos, known := storePosition(ctx, components.Store)
			if known && cadence.quiet(now, o.Days, pos) {
				logger.Debug("team upload skipped: no event changed since the last one")
			} else {
				err := uploadTeamOnce(ctx, deps, o, path, logger)
				cadence.done(now, o.Days, full, err)
				if known {
					cadence.sent(now, o.Days, pos, err)
				}
			}
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

// teamRecentDays is how many UTC days an upload between full ones covers:
// today, and yesterday, which a session running past midnight still adds
// to.
const teamRecentDays = 2

// teamCadence decides how many days each upload covers. A day before
// yesterday no longer changes, yet a full upload recomputes every covered
// day: hourly, that kept the daemon busy four of every sixty minutes. So
// the first upload after a start (a new version or config may compute
// differently), the first of each UTC day (prices and late reads settle)
// and the one after a failure cover all the days; the others only the
// last two. The server replaces only the days an upload names.
//
// An upload that was not sent because part of it could not be read
// (teamshare.IncompleteError) changes nothing here: the server has what it
// had, and the next upload owes the days this one did. Widening it to
// every day would make a read that ran out of time run out sooner.
type teamCadence struct {
	// full is when the last full upload succeeded; zero when none has.
	full time.Time
	// failed is whether the last upload did not reach the server.
	failed bool
	// sentAt, sentDays and sentPos are when the last upload the server
	// took was built, how many days it covered, and where the event store
	// stood as it was built.
	sentAt   time.Time
	sentDays int
	sentPos  sqlite.Position
}

// days is how many days the upload at now covers, of the full n.
func (c *teamCadence) days(now time.Time, n int) int {
	if c.failed || c.full.IsZero() || n <= teamRecentDays || !sameUTCDay(c.full, now) {
		return n
	}
	return teamRecentDays
}

// done notes how the upload at now, covering days of the full n, ended.
func (c *teamCadence) done(now time.Time, days, n int, err error) {
	if errors.As(err, new(*teamshare.IncompleteError)) {
		return
	}
	c.failed = err != nil
	if err == nil && days == n {
		c.full = now
	}
}

// storePosition is where store stands, and whether it could be read.
func storePosition(ctx context.Context, store *sqlite.Store) (sqlite.Position, bool) {
	if store == nil {
		return sqlite.Position{}, false
	}
	pos, err := store.Position(ctx)
	return pos, err == nil
}

// teamQuietMax is the longest an upload is skipped because the event
// store has not moved. The upload also reads transcripts, git and, on an
// owner's machine, a provider's cost API, which change without an event;
// those reach the team within this long.
const teamQuietMax = 6 * time.Hour

// quiet is whether the upload at now, covering days, can be skipped: the
// event store stands at pos, where it stood when the last upload was sent,
// that upload covered at least these days of the same UTC day, and it was
// sent less than teamQuietMax ago (ADR 0019). Overnight, with nobody
// working, each hourly upload rebuilt the same two days for minutes and
// sent the server what it already had.
func (c *teamCadence) quiet(now time.Time, days int, pos sqlite.Position) bool {
	return !c.failed && !c.sentAt.IsZero() && pos == c.sentPos && days <= c.sentDays &&
		sameUTCDay(c.sentAt, now) && now.Sub(c.sentAt) < teamQuietMax
}

// sent notes the store's position pos when the upload at now, covering
// days, was built; only an upload the server took counts.
func (c *teamCadence) sent(now time.Time, days int, pos sqlite.Position, err error) {
	if err != nil {
		return
	}
	c.sentAt, c.sentDays, c.sentPos = now, days, pos
}

func sameUTCDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}

// uploadTeamOnce builds and sends one upload; the error is nil only when
// the server took it.
func uploadTeamOnce(ctx context.Context, deps teamshare.Deps, opts teamshare.Options, path string, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, teamUploadTimeout)
	defer cancel()
	start := time.Now()
	parts := []any{}
	opts.Took = func(part string, took time.Duration) {
		parts = append(parts, slog.Duration(part, took.Round(time.Millisecond)))
	}
	res, err := teamshare.Sync(ctx, deps, opts, path, start)
	took := slog.Group("took", append([]any{slog.Duration("total", time.Since(start).Round(time.Millisecond))}, parts...)...)
	var incomplete *teamshare.IncompleteError
	switch {
	case errors.Is(err, teamclient.ErrNotJoined):
		return err
	case bcap.IsShareRefused(err):
		logger.Info("team upload held by a boundary", "detail", err.Error())
	case errors.As(err, new(*teamshare.PausedError)):
		// Billing, not a fault: say so plainly, and keep trying hourly so
		// uploads resume on their own once a subscription starts.
		logger.Info("team uploads paused by the organisation's billing", "detail", err.Error())
	case errors.As(err, &incomplete):
		// An upload replaces every day it covers, so one with a part
		// missing is held back: the team keeps the figures it has.
		logger.Warn("team upload not sent: part of the figures could not be read; the next one reads these days again",
			"unread", incomplete.Unread, took)
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
		logger.Warn("team upload: rows that failed validation were left out", "detail", w)
	}
	return err
}
