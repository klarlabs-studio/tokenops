package claudecodeoauth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/events"
)

// SourceTag marks readings from this source.
const SourceTag = "claude-code-oauth"

// DefaultInterval is how often usage is read. The endpoint rate-limits
// (CodexBar #575), so this polls half as often as the claude.ai meter.
const DefaultInterval = 10 * time.Minute

// keychainBackoff is how long a declined Keychain read is not asked again:
// a prompt the operator dismissed should not come back every poll.
const keychainBackoff = 6 * time.Hour

// PollerOptions configures the poller.
type PollerOptions struct {
	Stores []Store
	// Client asks Anthropic for the windows; the daemon passes the HTTP
	// client from internal/infra/vendorusage/claudecodeoauth.
	Client   UsageClient
	Interval time.Duration
	Health   *freshness.Recorder
	Logger   *slog.Logger
	// Now is injected for tests.
	Now func() time.Time
}

// Poller reads the plan's windows on a schedule and publishes each new
// reading. The token lives in memory only.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu           sync.Mutex
	creds        Credentials
	blockedUntil time.Time
	deniedAt     time.Time
	lastKey      string
}

// NewPoller returns a poller publishing to bus.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Poller{bus: bus, opts: opts}
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

// Scan reads usage once.
func (p *Poller) Scan(ctx context.Context) {
	now := p.opts.Now()
	if now.Before(p.blockedUntil) {
		return
	}
	creds, err := p.credentials(ctx, now, false)
	if err != nil {
		p.fail(err)
		return
	}
	usage, err := p.opts.Client.Usage(ctx, creds.AccessToken, now)
	if errors.Is(err, ErrExpired) {
		// Claude Code may have renewed it since it was read.
		if fresh, rerr := p.credentials(ctx, now, true); rerr == nil && fresh.AccessToken != creds.AccessToken {
			usage, err = p.opts.Client.Usage(ctx, fresh.AccessToken, now)
		}
	}
	var limited *RateLimitedError
	if errors.As(err, &limited) {
		p.blockedUntil = limited.Until
	}
	if err != nil {
		p.fail(err)
		return
	}
	p.succeed()
	if !usage.HasSignal() {
		return
	}
	env := claudeusagemeter.NewReading(SourceTag, now.UTC(), "", usage)
	key := readingKey(env.Attributes)
	if key == p.lastKey {
		return
	}
	p.lastKey = key
	if p.bus != nil {
		if err := p.bus.PublishWait(ctx, env); err != nil {
			p.opts.Logger.Warn("claude-code-oauth: reading not stored", "err", err)
		}
	}
}

// credentials returns the token in hand, re-reading the stores when there
// is none, it has expired, or reread asks. A declined Keychain is not
// asked again for keychainBackoff.
func (p *Poller) credentials(ctx context.Context, now time.Time, reread bool) (Credentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !reread && p.creds.AccessToken != "" && !p.creds.Expired(now) {
		return p.creds, nil
	}
	stores := p.opts.Stores
	if !p.deniedAt.IsZero() && now.Sub(p.deniedAt) < keychainBackoff {
		stores = withoutKeychain(stores)
	}
	c, err := ReadFirst(ctx, stores)
	if errors.Is(err, ErrKeychainDenied) {
		p.deniedAt = now
	}
	if err != nil {
		return Credentials{}, err
	}
	if c.Expired(now) {
		p.creds = Credentials{}
		return Credentials{}, ErrExpired
	}
	p.creds = c
	return c, nil
}

func withoutKeychain(stores []Store) []Store {
	out := make([]Store, 0, len(stores))
	for _, s := range stores {
		if ps, ok := s.(PromptingStore); !ok || !ps.Prompts() {
			out = append(out, s)
		}
	}
	return out
}

func (p *Poller) fail(err error) {
	p.opts.Health.Failed(err, p.opts.Now())
	p.opts.Logger.Warn("claude-code-oauth: usage not read", "err", err)
}

func (p *Poller) succeed() { p.opts.Health.Succeeded(p.opts.Now()) }

// readingKey identifies a reading's content, so an unchanged one is not
// stored again.
func readingKey(attrs map[string]string) string {
	return claudeusagemeter.AttributesKey(attrs)
}
