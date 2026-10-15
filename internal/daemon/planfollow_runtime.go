package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/planevidence"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// planFollowInterval is how often the daemon re-reads what the clients
// report about their plans, and which clients' sessions are on this
// machine. The reads are a few small local files and stats (Claude Code's
// ~/.claude.json, the newest Codex sessions, whether each local reader's
// store exists), and a change is rare, so a quarter of an hour bounds how
// long limits stay wrong or a client stays unread at a cost nobody notices.
const planFollowInterval = 15 * time.Minute

// planFollower rebinds a provider to the plan its client reports, and turns
// on the local reader of a client whose sessions appear, so neither change
// waits for anyone to re-run init.
type planFollower struct {
	configPath string
	home       string
	store      *sqlite.Store
	// apply makes a change take effect: the daemon reads its config once,
	// at boot.
	apply  func()
	logger *slog.Logger
	now    func() time.Time

	// running is the set of local readers this process started with.
	running map[string]bool
	// present lists the local readers whose data is on this machine.
	present func() []string
	// asked is the readers a restart was already asked for, so an
	// unsupervised daemon says it once rather than every pass.
	asked map[string]bool
}

// check follows what the clients report now, turns on the readers whose
// data appeared, logs each change once with the command that undoes it,
// and applies them with one restart.
func (f *planFollower) check(ctx context.Context) {
	followed := f.followPlans(ctx)
	adopted := f.followReaders()
	if followed || adopted {
		f.apply()
	}
}

func (f *planFollower) followPlans(ctx context.Context) bool {
	res, err := actions.FollowClientPlans(ctx, f.configPath, planevidence.All(f.home), f.store, f.now())
	if err != nil {
		f.logger.Warn("could not follow the plan a client reports; will retry", "err", err)
		return false
	}
	for _, c := range res.Kept {
		f.logger.Debug("plan binding kept: "+c.Note(), "provider", c.Provider, "plan", c.Plan, "reported", c.Reported)
	}
	for _, c := range res.Followed {
		f.logger.Info("plan binding follows the client: "+c.Note(),
			"provider", c.Provider, "plan", c.Plan, "previous", c.Previous, "client", c.Client, "since", c.Since)
	}
	return len(res.Followed) > 0
}

// followReaders turns on each local reader whose data is on this machine,
// unless the operator switched it off (ADR 0009, autonomous by default),
// and reports whether a reader is now on in the config that this process
// does not run — one it just turned on, or one `tokenops init` did.
func (f *planFollower) followReaders() bool {
	if f.present == nil {
		return false
	}
	cfg, err := config.ReadMutable(f.configPath)
	if err != nil {
		f.logger.Warn("could not read the config to check for new sessions; will retry", "err", err)
		return false
	}
	if turned := cfg.TurnOnLocalReaders(f.present()); len(turned) > 0 {
		if err := config.WriteMutable(f.configPath, cfg); err != nil {
			f.logger.Warn("could not turn on a reader whose sessions are on this machine; will retry", "readers", turned, "err", err)
			return false
		}
		f.logger.Info("turned on "+strings.Join(turned, ", ")+
			": their sessions are on this machine — `tokenops vendor-usage enable <source> --disable` to stop one",
			"readers", turned)
	}
	if f.asked == nil {
		f.asked = map[string]bool{}
	}
	pending := false
	for name := range cfg.EnabledLocalReaders() {
		if !f.running[name] && !f.asked[name] {
			f.asked[name] = true
			pending = true
		}
	}
	return pending
}

// startPlanFollowRuntime follows the clients' reported plans and the local
// readers' data at start and every planFollowInterval. It needs the config
// file the binding lives in, and never creates one.
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
	f := &planFollower{configPath: path, home: home, store: store, apply: applyPlanFollow(logger), logger: logger,
		now:     func() time.Time { return time.Now().UTC() },
		running: cfg.EnabledLocalReaders(), present: sourceprobe.PresentLocalReaders}
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

// applyPlanFollow restarts a supervised daemon so it reads the new config;
// an unsupervised one says how to.
func applyPlanFollow(logger *slog.Logger) func() {
	return func() {
		if !UnitInstalled() {
			logger.Warn("the config change is written; restart `tokenops start` to use it")
			return
		}
		if r := RestartForConfig(); r.Err != nil {
			logger.Warn("restart after a config change failed — run `tokenops daemon restart`", "err", r.Err)
		}
	}
}
