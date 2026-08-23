package daemon

import (
	"context"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// planStoreReader adapts *sqlite.Store to plans.EventReader without
// dragging the sqlite dependency into the domain package.
type planStoreReader struct{ store *sqlite.Store }

func (r planStoreReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return r.store.Query(ctx, sqlite.Filter{Type: t, Since: since, Limit: 100_000})
}

// windowProbe caches how full each provider's rate-limit window is.
//
// The router consults this on every proxied request, and computing it means
// scanning the event store over the plan's window — far too expensive to do
// inline. So a background loop refreshes it and the request path only ever
// reads a cached number.
//
// A reading is reported as untrustworthy until one has actually been taken.
// A router rule gated on window pressure must not fire on a default of
// zero, and must not fire on a stale value either: this meter read 0/200
// for months on a real machine, and acting on that would have degraded
// quality to relieve a shortage that was not happening.
type windowProbe struct {
	mu       sync.RWMutex
	readings map[eventschema.Provider]windowReading
}

type windowReading struct {
	pct float64
	at  time.Time
}

// maxWindowAge is how long a cached reading stays usable. Beyond it the
// probe reports "unknown" rather than a number nobody has refreshed.
const maxWindowAge = 10 * time.Minute

func newWindowProbe() *windowProbe {
	return &windowProbe{readings: map[eventschema.Provider]windowReading{}}
}

// Pct returns the cached window fill for a provider and whether it can be
// trusted.
func (p *windowProbe) Pct(provider eventschema.Provider) (float64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	r, ok := p.readings[provider]
	if !ok || time.Since(r.at) > maxWindowAge {
		return 0, false
	}
	return r.pct, true
}

func (p *windowProbe) set(provider eventschema.Provider, pct float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readings[provider] = windowReading{pct: pct, at: time.Now()}
}

// runWindowProbe refreshes the cached readings until ctx is cancelled.
func runWindowProbe(
	ctx context.Context,
	probe *windowProbe,
	cfg config.Config,
	reader plans.EventReader,
	interval time.Duration,
) {
	if interval <= 0 {
		interval = time.Minute
	}
	refresh := func() {
		for providerName, planName := range cfg.Plans {
			plan, ok := plans.Lookup(planName)
			if !ok {
				continue
			}
			provider := eventschema.Provider(providerName)
			pct, ok := windowPctFor(ctx, reader, provider, plan, time.Now().UTC())
			if !ok {
				continue
			}
			probe.set(provider, pct)
		}
	}
	refresh()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// windowPctFor resolves how full a provider's rate-limit window is.
//
// The vendor's own reading wins wherever one exists. Codex publishes a
// rate_limits block per turn, and Copilot and Cursor publish quota
// snapshots; those are ground truth. Counting plan-included messages and
// dividing by the catalog cap is a heuristic built for clients that
// publish nothing at all — using it where the vendor already answers
// would throw away the better signal and disagree with the number the
// operator sees in their own dashboard.
func windowPctFor(
	ctx context.Context,
	reader plans.EventReader,
	provider eventschema.Provider,
	plan plans.Plan,
	now time.Time,
) (float64, bool) {
	if plan.RateLimitWindow > 0 {
		if a := plans.LatestAuthoritativeWindow(ctx, reader, provider, plan, now); a != nil {
			return a.UsedPct, true
		}
	}
	if plan.RateLimitWindow <= 0 || plan.MessagesPerWindow <= 0 {
		return 0, false
	}
	consumption, err := plans.ConsumptionInWindow(
		ctx, reader, string(provider), now, plan.RateLimitWindow)
	if err != nil {
		return 0, false
	}
	return float64(consumption.MessagesInWindow) / float64(plan.MessagesPerWindow) * 100, true
}
