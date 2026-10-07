package fireworks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes this poller emits.
const SourceTag = "fireworks-usage"

// PollerOptions configures the reader.
type PollerOptions struct {
	// Health, when set, receives success and failure, so status can tell
	// a refused key from Fireworks not being used.
	Health *freshness.Recorder
	// Client reads the account; the daemon passes the HTTP client from
	// internal/infra/vendorusage/fireworks. Required.
	Client Reader
	// Home is where FireConnect's files are; empty uses $HOME.
	Home string
	// Interval defaults to 15 minutes. Fireworks aggregates billing
	// daily and refreshes user limits about once a minute.
	Interval time.Duration
	Logger   *slog.Logger
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Poller reads the Fireworks account's spend and limit on a tick and
// stores each changed reading as a quota snapshot.
type Poller struct {
	bus    events.Bus
	opts   PollerOptions
	client Reader

	mu        sync.Mutex
	last      string
	publishes int64
}

// NewPoller builds a poller.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 15 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Home == "" {
		opts.Home, _ = os.UserHomeDir()
	}
	return &Poller{bus: bus, opts: opts, client: opts.Client}
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) error {
	t := time.NewTicker(p.opts.Interval)
	defer t.Stop()
	p.Scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.Scan(ctx)
		}
	}
}

// Scan takes one reading. No key on the machine is not a failure: it is
// Fireworks not being used here.
func (p *Poller) Scan(ctx context.Context) {
	home := p.opts.Home
	account, user := Identity(home)
	now := p.opts.Now().UTC()
	r, err := p.client.Read(ctx, account, user, now)
	if errors.Is(err, ErrNoKey) {
		p.opts.Logger.Debug("fireworks: no API key on this machine; idle")
		return
	}
	if err != nil {
		p.opts.Health.Failed(err, now)
		p.opts.Logger.Warn("fireworks: reading failed", "err", err)
		return
	}
	p.opts.Health.Succeeded(now)
	env := NewEnvelope(now, r)
	key := fmt.Sprint(env.Attributes)
	p.mu.Lock()
	unchanged := key == p.last
	p.mu.Unlock()
	if unchanged || p.bus == nil {
		return
	}
	if err := p.bus.PublishWait(ctx, env); err != nil {
		p.opts.Logger.Warn("usage event not stored", "err", err)
		return
	}
	p.mu.Lock()
	p.last = key
	p.publishes++
	p.mu.Unlock()
}

// NewEnvelope stores a reading in the shape the Claude usage meter uses
// for a spend limit (extra_usage_*), so headroom reads both the same way.
// billing=per_token marks an account billed per token, which headroom may
// bind to pay-as-you-go when no plan is bound.
func NewEnvelope(ts time.Time, r Reading) *eventschema.Envelope {
	h := sha256.Sum256([]byte("fireworks-usage|" + r.AccountID + "|" + strconv.FormatInt(ts.UnixNano(), 10)))
	return &eventschema.Envelope{
		ID:            "fwu-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes: map[string]string{
			// A quota snapshot, not a request: window math skips it.
			"granularity":               "quota_snapshot",
			"billing":                   "per_token",
			"account_id":                r.AccountID,
			"scope":                     string(r.Scope),
			"extra_usage_used":          fmt.Sprintf("%.2f", r.UsedUSD),
			"extra_usage_limit":         fmt.Sprintf("%.2f", r.LimitUSD),
			"extra_usage_currency":      "USD",
			"extra_usage_limit_reached": strconv.FormatBool(r.LimitReached),
		},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderFireworks, Status: 200},
	}
}
