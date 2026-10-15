package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/budgets"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// boundaryFiguresEvery is how often the daemon measures the figures the
// pause point compares. The pause point counts them for five minutes.
const boundaryFiguresEvery = time.Minute

// budgetFiguresEvery is how often the budget windows are measured when no
// rule compares a figure: often enough for the coach's tips and the
// glance, which count them for fifteen minutes.
const budgetFiguresEvery = 5

// startBoundaryFiguresRuntime writes, every minute, the plan headroom and
// budget figures boundary rules compare (ADR 0014), so the pause point
// reads one small file instead of the event store. It measures every
// minute while a rule, local or the organisation's, compares one of them;
// otherwise, with budgets configured, it measures the budget windows every
// five minutes for the coach and the glance.
func startBoundaryFiguresRuntime(cfg config.Config, glance func() headroom.Deps, source budgets.Source, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if sup == nil {
		return
	}
	rules := bcap.LiveRules(cfg)
	limits := cfg.BudgetLimits()
	dir := boundaryledger.DefaultDir()
	ticks := 0
	measure := func(ctx context.Context) {
		live := rules()
		needs := bcap.NeedsFigures(live)
		ticks++
		hasBudgets := source != nil && len(limits) > 0
		if !needs && (!hasBudgets || (ticks-1)%budgetFiguresEvery != 0) {
			return
		}
		now := time.Now().UTC()
		var readings []budgets.Reading
		if hasBudgets {
			readings = budgets.Read(ctx, source, limits, now)
		}
		used := budgets.UsedPctOf(readings)
		fig := bcap.Figures(nil, used, now)
		// Plan headroom is read only when a rule compares it.
		if needs {
			if g, err := headroom.ComputeGlance(ctx, glance(), now); err == nil {
				fig = bcap.Figures(g.Headroom.Reports, used, now)
			} else if ctx.Err() == nil {
				logger.Warn("boundary figures: plan headroom unreadable; plan rules see no figure", "err", err)
			}
		}
		fig = bcap.WithBudgets(fig, readings, live)
		if err := dir.PutFigures(fig); err != nil {
			logger.Warn("boundary figures not written; plan and budget rules see no figure", "err", err)
		}
	}
	sup.Go("boundary-figures", func(ctx context.Context) error {
		tick := time.NewTicker(boundaryFiguresEvery)
		defer tick.Stop()
		for {
			measure(ctx)
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
		}
	})
}

// shareGateEvery is how often the event exporter's share boundary is
// asked again. Events arrive many times a second; one answer a minute is
// what an approval or a promotion needs to take effect.
const shareGateEvery = time.Minute

// sharedSink holds events from the OTel exporter while a share boundary
// refuses them. Held events are dropped, not queued: the boundary says
// they must not leave, and the event store keeps every one regardless.
type sharedSink struct {
	inner  events.Sink
	allow  func() error
	logger *slog.Logger

	mu      sync.Mutex
	checked time.Time
	held    bool
}

// gateShare wraps sink with the share boundary for destination, or
// returns it as is when no rule concerns sharing.
func gateShare(cfg config.Config, destination string, sink events.Sink, logger *slog.Logger) events.Sink {
	allow := shareAllowed(cfg, destination)
	if allow == nil {
		return sink
	}
	return &sharedSink{inner: sink, allow: allow, logger: logger}
}

func (s *sharedSink) AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error {
	if s.isHeld(time.Now()) {
		return nil
	}
	return s.inner.AppendBatch(ctx, envs)
}

func (s *sharedSink) isHeld(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.checked) < shareGateEvery {
		return s.held
	}
	s.checked = now
	err := s.allow()
	if held := err != nil; held != s.held {
		s.held = held
		if held {
			s.logger.Info("otlp event export held by a boundary", "detail", err.Error())
		} else {
			s.logger.Info("otlp event export resumed")
		}
	}
	return s.held
}
