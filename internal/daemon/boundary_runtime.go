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
	"go.klarlabs.de/tokenops/internal/infra/derived"
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
// five minutes for the coach and the glance. The figures follow the event
// log (ADR 0019): inputs reads where it stands.
func startBoundaryFiguresRuntime(cfg config.Config, glance func() headroom.Deps, bill budgets.Bill, source budgets.Source, inputs func(context.Context) (string, bool), sup *lifecycle.Supervisor, logger *slog.Logger) {
	if sup == nil {
		return
	}
	b := &boundaryFigures{
		rules: bcap.LiveRules(cfg), limits: cfg.BudgetLimits(), source: source, bill: bill, glance: glance,
		dir: boundaryledger.DefaultDir(), inputs: inputs, maxAge: cfg.Storage.EffectiveDerivedMaxAge(), logger: logger,
	}
	sup.Go("boundary-figures", func(ctx context.Context) error {
		tick := time.NewTicker(boundaryFiguresEvery)
		defer tick.Stop()
		for {
			b.measure(ctx, time.Now().UTC())
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
		}
	})
}

// boundaryFigures measures the figures the pause point compares. The plan
// headroom and the budget windows are read from events; they are read
// again only when the inputs moved, a boundary they hold passed (a
// window's reset), or they reached maxAge, and kept in between.
type boundaryFigures struct {
	rules  func() []bcap.Rule
	limits []budgets.Limit
	source budgets.Source
	bill   budgets.Bill
	glance func() headroom.Deps
	dir    boundaryledger.Dir
	// inputs is where the figures' inputs other than the clock stand; nil
	// reads them on every measurement.
	inputs func(context.Context) (string, bool)
	maxAge time.Duration
	logger *slog.Logger

	ticks int
	last  figuresRead
}

// figuresRead is what one measurement read from events.
type figuresRead struct {
	reports  []headroom.Report
	readings []budgets.Reading
	// head is whether the plan headroom was read: only while a rule
	// compares it.
	head bool
	// at is the now they were read for, good until until; in is the
	// inputs they were read from, and kept whether they may be kept.
	at, until time.Time
	in        string
	kept      bool
}

// measure writes the figures for now, when they are due.
func (b *boundaryFigures) measure(ctx context.Context, now time.Time) {
	live := b.rules()
	needs := bcap.NeedsFigures(live)
	b.ticks++
	hasBudgets := b.source != nil && len(b.limits) > 0
	if !needs && (!hasBudgets || (b.ticks-1)%budgetFiguresEvery != 0) {
		return
	}
	in, known := "", false
	if b.inputs != nil {
		in, known = b.inputs(ctx)
	}
	r := b.last
	if !r.kept || r.head != needs || !derived.Keep(now, r.at, r.until, r.in, in, known) {
		r = b.read(ctx, needs, hasBudgets, now)
		r.in, r.kept = in, r.kept && known
		b.last = r
	}
	// Plan headroom is read only when a rule compares it, and only the
	// headroom: the full glance (session budgets, plan use, a month's
	// plan advice) cost seconds and hundreds of MB a minute (#825).
	fig := bcap.Figures(r.reports, budgets.UsedPctOf(r.readings), now)
	// Observed now: nothing they read has changed since they were
	// computed, and no boundary they hold has passed.
	fig.ComputedAt = r.at
	fig = bcap.WithBudgets(fig, r.readings, live)
	if err := b.dir.PutFigures(fig); err != nil {
		b.logger.Warn("boundary figures not written; plan and budget rules see no figure", "err", err)
	}
}

// read reads the figures from events for now. A headroom that could not
// be read is not kept: the next measurement tries again.
func (b *boundaryFigures) read(ctx context.Context, needs, hasBudgets bool, now time.Time) figuresRead {
	r := figuresRead{head: needs, at: now, kept: true}
	if hasBudgets {
		r.readings = budgets.ReadBilled(ctx, b.source, b.bill, b.limits, now)
	}
	if needs {
		if head, err := headroom.Compute(ctx, b.glance(), now); err == nil {
			r.reports = head.Reports
		} else {
			r.kept = false
			if ctx.Err() == nil {
				b.logger.Warn("boundary figures: plan headroom unreadable; plan rules see no figure", "err", err)
			}
		}
	}
	r.until = derived.Until(now, b.maxAge, nil, r.reports, r.readings)
	return r
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
