package daemon

import (
	"context"
	"log/slog"
	"os"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/planevidence"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// planFollowInterval is how often the daemon re-reads what the clients
// report about their plans. The reads are two small local files (Claude
// Code's ~/.claude.json, the newest Codex sessions), and a plan change is
// rare, so a quarter of an hour bounds how long limits stay wrong at a
// cost nobody notices.
const planFollowInterval = 15 * time.Minute

// planFollower rebinds a provider to the plan its client reports, so a
// plan change reaches the daemon without anyone re-running init.
type planFollower struct {
	configPath string
	home       string
	store      *sqlite.Store
	// apply makes a new binding take effect: the daemon reads its config
	// once, at boot.
	apply  func()
	logger *slog.Logger
	now    func() time.Time
}

// check follows what the clients report now, logs each change once with
// the command that undoes it, and applies it.
func (f planFollower) check(ctx context.Context) {
	res, err := actions.FollowClientPlans(ctx, f.configPath, planevidence.All(f.home), f.store, f.now())
	if err != nil {
		f.logger.Warn("could not follow the plan a client reports; will retry", "err", err)
		return
	}
	for _, c := range res.Kept {
		f.logger.Debug("plan binding kept: "+c.Note(), "provider", c.Provider, "plan", c.Plan, "reported", c.Reported)
	}
	for _, c := range res.Followed {
		f.logger.Info("plan binding follows the client: "+c.Note(),
			"provider", c.Provider, "plan", c.Plan, "previous", c.Previous, "client", c.Client, "since", c.Since)
	}
	if len(res.Followed) > 0 {
		f.apply()
	}
}

// startPlanFollowRuntime follows the clients' reported plans at start and
// every planFollowInterval. It needs the config file the binding lives
// in, and never creates one.
func startPlanFollowRuntime(cfg config.Config, store *sqlite.Store, sup *lifecycle.Supervisor, logger *slog.Logger) {
	path := cfg.SourcePath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return
		}
		path = p
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	f := planFollower{configPath: path, home: home, store: store, apply: applyPlanFollow(logger), logger: logger,
		now: func() time.Time { return time.Now().UTC() }}
	sup.Go("plan-follow", func(ctx context.Context) error {
		ticker := time.NewTicker(planFollowInterval)
		defer ticker.Stop()
		for {
			f.check(ctx)
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	})
}

// applyPlanFollow restarts a supervised daemon so it reads the new
// binding; an unsupervised one says how to.
func applyPlanFollow(logger *slog.Logger) func() {
	return func() {
		if !UnitInstalled() {
			logger.Warn("the new plan binding is written; restart `tokenops start` to use it")
			return
		}
		if r := RestartForConfig(); r.Err != nil {
			logger.Warn("restart after following a client's plan failed — run `tokenops daemon restart`", "err", r.Err)
		}
	}
}
