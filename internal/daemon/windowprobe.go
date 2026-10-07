package daemon

import (
	"context"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

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

// runWindowProbe refreshes the cached readings until ctx is cancelled,
// through the headroom capability the MCP routing advice also asks.
func runWindowProbe(
	ctx context.Context,
	probe *windowProbe,
	cfg config.Config,
	reader headroom.EventReader,
	interval time.Duration,
) {
	if interval <= 0 {
		interval = time.Minute
	}
	refresh := func() {
		for providerName := range cfg.Plans {
			provider := eventschema.Provider(providerName)
			if pct, ok := headroom.WindowPressure(ctx, &cfg, reader, provider, time.Now().UTC()); ok {
				probe.set(provider, pct)
			}
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
