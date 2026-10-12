package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
)

// Organisation boundaries (ADR 0014 §4–5): how often the daemon routes
// waiting approvals to the team plane and reads the answers, and how many
// of those ticks pass between fetches of the organisation's rules.
const (
	teamApprovalsEvery = 30 * time.Second
	teamRulesEveryN    = 10
)

// startTeamBoundariesRuntime keeps the organisation's rules and approvals
// in step with the team plane this machine joined. It reads the enrolment
// on every tick, so join and leave take effect without a restart; a
// machine that is not joined holds no organisation rules, and any it held
// are forgotten. Uploads being switched off does not stop it: the
// organisation's rules bind whether or not figures are sent.
func startTeamBoundariesRuntime(cfg config.Config, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if sup == nil {
		return
	}
	path, err := teamclient.ResolveStatePath(cfg.Team.StatePath)
	if err != nil {
		logger.Warn("organisation boundaries not started", "err", err)
		return
	}
	t := &teamBoundaries{path: path, dir: boundaryledger.DefaultDir(), logger: logger}
	sup.Go("team-boundaries", func(ctx context.Context) error {
		tick := time.NewTicker(teamApprovalsEvery)
		defer tick.Stop()
		for n := 0; ; n++ {
			t.once(ctx, n%teamRulesEveryN == 0)
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
		}
	})
}

type teamBoundaries struct {
	path    string
	dir     boundaryledger.Dir
	logger  *slog.Logger
	version int64
	lastErr string
}

func (t *teamBoundaries) once(ctx context.Context, fetch bool) {
	st, err := teamclient.Load(t.path)
	if errors.Is(err, teamclient.ErrNotJoined) {
		if t.dir.OrgModTime().IsZero() {
			return
		}
		if err := t.dir.RemoveOrg(); err == nil {
			t.logger.Info("organisation boundaries forgotten: this machine is no longer in a team")
		}
		t.version = 0
		return
	}
	if err != nil {
		t.report("team state unreadable", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	c := teamclient.New(st.URL, st.DeviceToken)
	if fetch || t.version == 0 {
		v, err := bcap.SyncOrg(ctx, c, t.dir, t.path)
		if err != nil {
			t.report("organisation boundaries not fetched; the rules held still apply", err)
		} else if v != t.version {
			t.logger.Info("organisation boundaries", "version", v)
			t.version = v
		}
	}
	if err := bcap.RouteApprovals(ctx, c, bcap.Env{Dir: t.dir}, t.version); err != nil {
		t.report("approval requests not sent or read; the actions stay held", err)
		return
	}
	t.lastErr = ""
}

// report logs a failure once until it changes, rather than every tick.
func (t *teamBoundaries) report(msg string, err error) {
	if err.Error() == t.lastErr {
		return
	}
	t.lastErr = err.Error()
	t.logger.Warn(msg, "err", err)
}
