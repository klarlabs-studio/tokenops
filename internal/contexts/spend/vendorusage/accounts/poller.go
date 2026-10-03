package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// PollerOptions configures the account readers.
type PollerOptions struct {
	// Credentials finds the keys, freshly on every scan, so a key added or
	// rotated in a harness is picked up.
	Credentials func() []Credential
	// Readers defaults to Readers().
	Readers []Reader
	// Health returns the recorder for a source tag; nil disables it.
	Health func(source string) *freshness.Recorder
	// Interval defaults to 15 minutes.
	Interval time.Duration
	Logger   *slog.Logger
	Now      func() time.Time
}

// Poller reads every vendor account a key is found for.
type Poller struct {
	bus  events.Bus
	opts PollerOptions
	mu   sync.Mutex
	last map[string]string
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
	if opts.Readers == nil {
		opts.Readers = Readers()
	}
	if opts.Health == nil {
		opts.Health = func(string) *freshness.Recorder { return nil }
	}
	return &Poller{bus: bus, opts: opts, last: map[string]string{}}
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

// Scan reads each vendor whose key is on the machine. Several keys for
// one vendor are tried in order until one is accepted; a vendor with no
// key is not in use here and is skipped silently.
func (p *Poller) Scan(ctx context.Context) {
	creds := p.opts.Credentials()
	now := p.opts.Now().UTC()
	for _, r := range p.opts.Readers {
		var (
			reading Reading
			err     error
			tried   bool
		)
		for _, c := range creds {
			if c.Endpoint != r.Endpoint() {
				continue
			}
			tried = true
			if reading, err = r.Read(ctx, c.Key); err == nil {
				break
			}
		}
		if !tried {
			continue
		}
		health := p.opts.Health(r.Source())
		if err != nil {
			health.Failed(err, now)
			if !errors.Is(err, ErrAuth) {
				p.opts.Logger.Warn("vendor account reading failed", "source", r.Source(), "err", err)
			}
			continue
		}
		health.Succeeded(now)
		p.publish(ctx, r, NewEnvelope(now, r, reading))
	}
}

func (p *Poller) publish(ctx context.Context, r Reader, env *eventschema.Envelope) {
	key := fmt.Sprint(env.Attributes)
	p.mu.Lock()
	unchanged := p.last[r.Source()] == key
	p.mu.Unlock()
	if unchanged || p.bus == nil {
		return
	}
	if err := p.bus.PublishWait(ctx, env); err != nil {
		p.opts.Logger.Warn("usage event not stored", "err", err)
		return
	}
	p.mu.Lock()
	p.last[r.Source()] = key
	p.mu.Unlock()
}

// NewEnvelope stores a reading as a quota snapshot with the spend-limit
// attributes headroom reads (extra_usage_*), plus balance_usd for prepaid
// credit. billing=per_token lets headroom bind pay-as-you-go unasked.
func NewEnvelope(ts time.Time, r Reader, x Reading) *eventschema.Envelope {
	h := sha256.Sum256([]byte(r.Source() + "|" + strconv.FormatInt(ts.UnixNano(), 10)))
	attrs := map[string]string{
		"granularity":               "quota_snapshot",
		"billing":                   "per_token",
		"scope":                     x.Scope,
		"extra_usage_limit":         fmt.Sprintf("%.2f", x.LimitUSD),
		"extra_usage_currency":      "USD",
		"extra_usage_limit_reached": strconv.FormatBool(x.LimitReached),
	}
	if x.HasUsed {
		attrs["extra_usage_used"] = fmt.Sprintf("%.2f", x.UsedUSD)
	}
	if x.HasBalance {
		attrs["balance_usd"] = fmt.Sprintf("%.2f", x.BalanceUSD)
	}
	return &eventschema.Envelope{
		ID:            "acct-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        r.Source(),
		Attributes:    attrs,
		Payload:       &eventschema.PromptEvent{Provider: r.Provider(), Status: 200},
	}
}
