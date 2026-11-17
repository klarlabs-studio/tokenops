package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/effortweeks"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/compactlever"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// The week's waste report reads five weeks of turns, every workflow of
// the week and git history, so it runs in the background: a few minutes
// after start, once ingestion has caught up, and once a day after. The
// glance's weekly headline reads the stored answer.
const (
	avoidableDelay = 10 * time.Minute
	avoidableEvery = 24 * time.Hour
)

func startAvoidableRuntime(cfg config.Config, c *bootstrap.Components, readers func(*avoidable.Deps), sup *lifecycle.Supervisor, logger *slog.Logger) {
	if c == nil || c.Aggregator == nil {
		return
	}
	d := avoidable.DepsFor(cfg, c.Aggregator, c.Store, c.Spend)
	if readers != nil {
		readers(&d)
	}
	var log avoidable.WeekLog
	if c.Store != nil {
		log = c.Store
	}
	after := func(ctx context.Context, s avoidable.Snapshot, now time.Time) {
		afterAvoidable(ctx, cfg, d, log, findings.DefaultDir(), s, now, logger)
	}
	sup.Go("avoidable-week", func(ctx context.Context) error {
		runAvoidable(ctx, d, findings.DefaultDir(), avoidableDelay, avoidableEvery, after, logger)
		return nil
	})
}

// afterAvoidable runs what the week's report feeds, once it is stored: the
// compaction setting's weekly rollup (ADR 0021), cheap because a finished
// week is computed once, and the context power's step at the ask rung,
// which proposes the measured line or writes an accepted one.
func afterAvoidable(ctx context.Context, cfg config.Config, d avoidable.Deps, log avoidable.WeekLog, dir string, s avoidable.Snapshot, now time.Time, logger *slog.Logger) {
	var line int64
	if s.Report.ContextLine != nil {
		line = s.Report.ContextLine.Tokens
	}
	var setAt time.Time
	if p, err := compactlever.DefaultPaths(); err == nil {
		setAt, _ = compactlever.SetSince(p)
	}
	if log != nil {
		figures := func(ctx context.Context, start time.Time, line int64) (avoidable.WeekFigs, error) {
			return avoidable.WeekFigures(ctx, d, start, line)
		}
		if _, err := avoidable.RefreshWeeks(ctx, log, figures, dir, line, setAt, now); err != nil {
			logger.Warn("compaction weeks not stored", "err", err)
		}
		refreshEffortWeeks(ctx, d, log, dir, now, logger)
	}
	ledger, levers := coachEnv(cfg)
	for _, r := range coachcap.StepCompaction(cfg, coachcap.AskEnv{
		Ledger: ledger, Shadow: savings.DefaultLedger(), Levers: levers,
		Graduated: func() bool { return coachcap.CompactionGraduated(ledger, now) },
	}, now) {
		logger.Info("compaction at the ask rung", "client", r.Client, "status", r.Status)
	}
}

func runAvoidable(ctx context.Context, d avoidable.Deps, dir string, delay, every time.Duration,
	after func(context.Context, avoidable.Snapshot, time.Time), logger *slog.Logger) {
	wait := time.NewTimer(delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	run := func() {
		start := time.Now()
		s, err := avoidable.Run(ctx, d, dir, start)
		if err != nil {
			logger.Warn("waste report not stored", "err", err)
			return
		}
		logger.Info("waste report stored", "took", time.Since(start).Round(time.Millisecond),
			"avoidable_tokens", s.Report.Avoidable.Tokens)
		if after != nil {
			after(ctx, s, start)
		}
	}
	run()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}

// refreshEffortWeeks keeps each model's weekly series per effort level, so
// a change of the level a model mostly runs at is judged on the
// developer's own weeks (ADR 0022). A finished week is computed once.
func refreshEffortWeeks(ctx context.Context, d avoidable.Deps, log avoidable.WeekLog, dir string, now time.Time, logger *slog.Logger) {
	if d.Records == nil {
		return
	}
	figures := func(ctx context.Context, start time.Time) (effortweeks.Week, error) {
		return effortweeks.Figures(ctx, d.Records, d.Turns, start)
	}
	if _, err := effortweeks.Refresh(ctx, log, figures, dir, now); err != nil {
		logger.Warn("effort weeks not stored", "err", err)
	}
}
