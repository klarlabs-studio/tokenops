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
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pollnow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// PollerOptions configures the account readers.
type PollerOptions struct {
	// Credentials finds the keys, freshly on every scan, so a key added or
	// rotated in a harness is picked up.
	Credentials func() []Credential
	// Readers are the vendors read; the daemon passes the HTTP readers
	// from internal/infra/vendorusage/accounts. nil reads none.
	Readers []Reader
	// Gateways are the gateways recognised; the daemon passes them from
	// internal/infra/vendorusage/accounts. nil recognises none.
	Gateways []Gateway
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
	// asked is when each paced reader was last sent a credential.
	asked map[string]time.Time
	// recognised caches which gateway each root is, nil for none.
	recognised map[string]recognition
}

// recognition is what a gateway root turned out to be, and when.
type recognition struct {
	g  Gateway
	at time.Time
}

// recogniseFor is how long a root's recognition is trusted: a gateway
// is rarely swapped for another at the same address.
const recogniseFor = 6 * time.Hour

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
	if opts.Health == nil {
		opts.Health = func(string) *freshness.Recorder { return nil }
	}
	return &Poller{bus: bus, opts: opts, last: map[string]string{}, asked: map[string]time.Time{}, recognised: map[string]recognition{}}
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) error {
	t := pollnow.NewTicker(ctx, p.opts.Interval)
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
// key is not in use here and is skipped silently. A Keyless reader is read
// without a key, and skipped silently when its CLI or app is not installed.
func (p *Poller) Scan(ctx context.Context) {
	creds := p.opts.Credentials()
	now := p.opts.Now().UTC()
	for _, r := range p.opts.Readers {
		if !p.due(r, creds, now) {
			continue
		}
		var (
			reading Reading
			err     error
			tried   bool
		)
		if IsKeyless(r) {
			reading, err = r.Read(ctx, "")
			tried = !errors.Is(err, ErrNotInstalled)
		} else {
			reading, tried, err = readWithKeys(ctx, r, creds)
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
		if reading.Empty() {
			continue
		}
		p.publish(ctx, r, NewEnvelope(now, r, reading))
	}
	p.scanGateways(ctx, creds, now)
}

// readWithKeys tries each credential found for r's endpoint, in order,
// until one is accepted. tried is false when none was found. A credential
// the reader declines (ErrSkip) does not count as tried, and a KeyOnly
// reader is never handed a credential that would re-read a browser.
func readWithKeys(ctx context.Context, r Reader, creds []Credential) (reading Reading, tried bool, err error) {
	keyOnly := false
	if k, ok := r.(KeyOnly); ok {
		keyOnly = k.KeyOnly()
	}
	for _, c := range creds {
		if c.Endpoint != r.Endpoint() {
			continue
		}
		key := c.Key
		if key == "" && c.Resolve != nil {
			if keyOnly {
				continue
			}
			resolved, rerr := c.Resolve(ctx)
			if rerr != nil || resolved == "" {
				if rerr == nil {
					rerr = ErrAuth
				}
				tried, err = true, withRemedy(rerr, c)
				continue
			}
			key = resolved
		}
		got, rerr := r.Read(ctx, key)
		if errors.Is(rerr, ErrSkip) {
			continue
		}
		tried, reading, err = true, got, withRemedy(rerr, c)
		if err == nil {
			break
		}
	}
	return reading, tried, err
}

// withRemedy adds the credential's remedy to a refusal, so the source's
// health says what to do about it.
func withRemedy(err error, c Credential) error {
	if err == nil || c.Remedy == "" || !errors.Is(err, ErrAuth) {
		return err
	}
	return fmt.Errorf("%w; %s", err, c.Remedy)
}

// scanGateways reads each gateway a key is sent to. A root is recognised
// without the key, by its health route; the key then goes to that root
// only, which is where the harness already sends it.
func (p *Poller) scanGateways(ctx context.Context, creds []Credential, now time.Time) {
	done := map[string]bool{}
	for _, c := range creds {
		if c.Endpoint != GatewayEndpoint {
			continue
		}
		root, g := p.gatewayFor(ctx, c, now)
		if g == nil || done[root] {
			continue
		}
		reading, err := g.Read(ctx, root, c.Key)
		health := p.opts.Health(g.Source())
		if err != nil {
			health.Failed(err, now)
			if !errors.Is(err, ErrAuth) {
				p.opts.Logger.Warn("gateway account reading failed", "source", g.Source(), "err", err)
			}
			continue
		}
		done[root] = true
		health.Succeeded(now)
		if reading.Empty() {
			continue
		}
		r := gatewayReader{g}
		p.publish(ctx, r, NewEnvelope(now, r, reading))
	}
}

// recognise returns the gateway at root, asking it at most once per
// recogniseFor.
// due reports whether r is to be read on this scan: always, unless it is
// paced and was asked within its interval. A paced reader with a
// credential to try counts as asked, read or refused, so a vendor that
// bills each request is billed no more often than that.
func (p *Poller) due(r Reader, creds []Credential, now time.Time) bool {
	paced, ok := r.(Paced)
	if !ok {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if last, ok := p.asked[r.Source()]; ok && now.Sub(last) < paced.MinInterval() {
		return false
	}
	for _, c := range creds {
		if c.Endpoint == r.Endpoint() {
			p.asked[r.Source()] = now
			break
		}
	}
	return true
}

// gatewayFor is the address a gateway credential is read at and the
// gateway that reads it: the one it was named for, at the base URL given,
// or the one recognised at the root of the harness's base URL.
func (p *Poller) gatewayFor(ctx context.Context, c Credential, now time.Time) (string, Gateway) {
	if c.Gateway != "" {
		base, ok := NamedGatewayBase(c.BaseURL)
		if !ok {
			return "", nil
		}
		for _, g := range p.opts.Gateways {
			if g.Name() == c.Gateway {
				return base, g
			}
		}
		return "", nil
	}
	root, ok := gatewayRoot(c.BaseURL)
	if !ok {
		return "", nil
	}
	return root, p.recognise(ctx, root, now)
}

func (p *Poller) recognise(ctx context.Context, root string, now time.Time) Gateway {
	p.mu.Lock()
	cached, ok := p.recognised[root]
	p.mu.Unlock()
	if ok && now.Sub(cached.at) < recogniseFor {
		return cached.g
	}
	var found Gateway
	for _, g := range p.opts.Gateways {
		if g.Recognise(ctx, root) {
			found = g
			break
		}
	}
	p.mu.Lock()
	p.recognised[root] = recognition{g: found, at: now}
	p.mu.Unlock()
	return found
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

// NewEnvelope stores a reading as a quota snapshot. A per-token account
// carries the spend-limit attributes headroom reads (extra_usage_*) and
// billing=per_token, which lets headroom bind pay-as-you-go unasked; a
// subscription carries billing=subscription and its windows as
// window_<n>_{name,used_pct,duration_min,reset_at}. Prepaid credit is
// balance_usd either way; credit in a unit that is not dollars is
// balance_credits with balance_credits_unit.
func NewEnvelope(ts time.Time, r Reader, x Reading) *eventschema.Envelope {
	h := sha256.Sum256([]byte(r.Source() + "|" + strconv.FormatInt(ts.UnixNano(), 10)))
	attrs := map[string]string{
		"granularity": "quota_snapshot",
		"scope":       x.Scope,
	}
	if x.Subscription {
		attrs["billing"] = "subscription"
	} else {
		attrs["billing"] = "per_token"
		attrs["extra_usage_limit"] = fmt.Sprintf("%.2f", x.LimitUSD)
		attrs["extra_usage_currency"] = "USD"
		attrs["extra_usage_limit_reached"] = strconv.FormatBool(x.LimitReached)
		if x.HasUsed {
			attrs["extra_usage_used"] = fmt.Sprintf("%.2f", x.UsedUSD)
		}
	}
	if x.HasBalance {
		attrs["balance_usd"] = fmt.Sprintf("%.2f", x.BalanceUSD)
	}
	if x.HasCredits {
		attrs["balance_credits"] = strconv.FormatFloat(x.Credits, 'f', -1, 64)
		attrs["balance_credits_unit"] = x.CreditsUnit
	}
	for i, w := range x.Windows {
		k := "window_" + strconv.Itoa(i) + "_"
		attrs[k+"name"] = w.Name
		attrs[k+"used_pct"] = fmt.Sprintf("%.2f", w.UsedPct)
		if w.Duration > 0 {
			attrs[k+"duration_min"] = strconv.Itoa(int(w.Duration / time.Minute))
		}
		if !w.ResetsAt.IsZero() {
			attrs[k+"reset_at"] = w.ResetsAt.UTC().Format(time.RFC3339)
		}
	}
	for i, c := range x.Counts {
		k := "count_" + strconv.Itoa(i) + "_"
		attrs[k+"name"] = c.Name
		attrs[k+"used"] = strconv.FormatFloat(c.Used, 'f', -1, 64)
		if !c.ResetsAt.IsZero() {
			attrs[k+"reset_at"] = c.ResetsAt.UTC().Format(time.RFC3339)
		}
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
