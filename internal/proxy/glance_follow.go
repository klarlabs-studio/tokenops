package proxy

import (
	"context"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/infra/derived"
)

// GlanceInputs reports where everything the glance's figures read, other
// than the clock, stands: the event store's position, the config they
// are computed under, and any file or rate card they read. Equal answers
// mean figures computed under one are computed the same under the other,
// but for the clock (ADR 0019). ok is false when it cannot be told; the
// figures are then computed again.
type GlanceInputs func(ctx context.Context) (token string, ok bool)

// WithGlanceFollows has the glance's figures (plan headroom, session
// budgets, plan use and advice, the prompt cache, provider costs) follow
// their inputs instead of the clock: a glance that is due computes them
// again only once inputs moved, a boundary they show passed (a window's
// reset, an expiry, a midnight), or maxAge passed. In between it answers
// the figures it has, and computed_at says when they were computed for.
// What the glance reads besides events (the coach, the findings, the spend
// budgets, the signed-in accounts) is read on every computation as before.
func WithGlanceFollows(inputs GlanceInputs, maxAge time.Duration) Option {
	return func(s *Server) {
		if inputs != nil && maxAge > 0 {
			s.glanceFollow = &glanceFollow{inputs: inputs, maxAge: maxAge}
		}
	}
}

// glanceFigures is the part of the glance computed from events: what the
// glance reads weeks of events for.
type glanceFigures struct {
	Glance    headroom.Glance
	Costs     map[string]spending.ProviderCost
	CostsLate bool
	// At is the now they were computed for.
	At time.Time
}

// glanceFollow keeps the last figures and the inputs they were computed
// from.
type glanceFollow struct {
	inputs GlanceInputs
	maxAge time.Duration

	mu    sync.Mutex
	last  glanceFigures
	in    string
	until time.Time
	ok    bool
}

// figures is the glance's figures for now: the last ones while
// derived.Keep holds, otherwise compute's. A nil f always computes.
func (f *glanceFollow) figures(ctx context.Context, now time.Time, compute func(context.Context, time.Time) (glanceFigures, error)) (glanceFigures, error) {
	if f == nil {
		return compute(ctx, now)
	}
	// Read before computing: an event that lands during the computation
	// moves the inputs past what is recorded, and the next one sees it.
	in, known := f.inputs(ctx)
	f.mu.Lock()
	if f.ok && derived.Keep(now, f.last.At, f.until, f.in, in, known) {
		v := f.last
		f.mu.Unlock()
		return v, nil
	}
	f.mu.Unlock()
	v, err := compute(ctx, now)
	if err != nil {
		return glanceFigures{}, err
	}
	f.mu.Lock()
	f.last, f.in, f.ok, f.until = v, in, known, figuresUntil(v, f.maxAge)
	f.mu.Unlock()
	return v, nil
}

// figuresUntil is the latest moment v stays what a computation would give
// with no input moved, but for time-only drift (see
// storage.derived_max_age): the earliest of every time v shows that lies
// ahead of v.At (a window's reset, a reading's freshness, an expiry, a
// snooze), the prompt cache turning, a midnight, and v.At plus maxAge.
func figuresUntil(v glanceFigures, maxAge time.Duration) time.Time {
	return derived.Until(v.At, maxAge, v.Glance.PromptCacheTurns(), v.Glance.Payload(), v.Costs)
}
