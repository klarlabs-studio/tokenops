package proxy

import (
	"context"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
)

// glanceRefreshBudget bounds one background computation of the glance.
const glanceRefreshBudget = 5 * time.Minute

// glanceFresh is how long a computed glance is answered without starting a
// newer one. The menu bar reads every minute.
const glanceFresh = 30 * time.Second

// glanceSnapshots keeps the glance off the request path. A glance replays
// weeks of events several times over; on a store with a month of heavy
// Codex use it takes longer than the menu bar waits, and a request that
// computed it showed nothing. Instead every request is answered from the
// last glance computed, at once, while a stale one is recomputed in the
// background. Only the first request, with nothing computed yet, waits.
// /api/glance and /api/findings share it, so one menu bar refresh
// computes the glance once, not twice.
type glanceSnapshots struct {
	compute func(context.Context, time.Time) (headroom.Glance, error)
	fresh   time.Duration

	mu sync.Mutex
	g  headroom.Glance
	at time.Time // the now it was computed for
	ok bool
	// running is open while a computation runs; err is how the last one
	// ended.
	running chan struct{}
	err     error
}

func newGlanceSnapshots(compute func(context.Context, time.Time) (headroom.Glance, error), fresh time.Duration) *glanceSnapshots {
	return &glanceSnapshots{compute: compute, fresh: fresh}
}

// get is the glance for a request at now: the snapshot when there is one,
// starting a newer computation when it is stale; otherwise it waits for
// the first one, or for ctx.
func (c *glanceSnapshots) get(ctx context.Context, now time.Time) (headroom.Glance, error) {
	c.mu.Lock()
	if c.ok && now.Sub(c.at) < c.fresh {
		g := c.g
		c.mu.Unlock()
		return g, nil
	}
	if c.running == nil {
		c.start(now)
	}
	if c.ok {
		g := c.g
		c.mu.Unlock()
		return g, nil
	}
	running := c.running
	c.mu.Unlock()

	select {
	case <-running:
	case <-ctx.Done():
		return headroom.Glance{}, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ok {
		return headroom.Glance{}, c.err
	}
	return c.g, nil
}

// start computes the glance for now in the background. c.mu is held.
// The computation outlives the request that started it: its result serves
// the next one.
func (c *glanceSnapshots) start(now time.Time) {
	done := make(chan struct{})
	c.running = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), glanceRefreshBudget)
		g, err := c.compute(ctx, now)
		cancel()
		c.mu.Lock()
		if err == nil {
			c.g, c.at, c.ok = g, now, true
		}
		c.err, c.running = err, nil
		c.mu.Unlock()
		close(done)
	}()
}

// waitIdle returns once no computation is running, for tests.
func (c *glanceSnapshots) waitIdle() {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if running != nil {
		<-running
	}
}

// glance is the glance for a request at now, from the plan routes' deps.
func (s *Server) glance(ctx context.Context, now time.Time) (headroom.Glance, error) {
	s.glancesOnce.Do(func() {
		s.glances = newGlanceSnapshots(func(ctx context.Context, now time.Time) (headroom.Glance, error) {
			return headroom.ComputeGlance(ctx, s.plans(), now)
		}, glanceFresh)
	})
	return s.glances.get(ctx, now)
}
