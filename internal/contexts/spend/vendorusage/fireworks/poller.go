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
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pollnow"
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
	// Home is where Nexus CLI's files are; empty uses $HOME.
	Home string
	// UserID, when set, is the member whose own cap is read, in place of
	// the user Nexus CLI's files name or the sign-in email finds.
	UserID string
	// Email, when set, is the sign-in email the user is found by, in place
	// of the one Nexus CLI's files hold.
	Email string
	// Finder finds the user by email when no file names them; nil leaves
	// the reading account-wide. The daemon passes the HTTP client.
	Finder UserFinder
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
	found     foundUser
}

// foundUser is a user found by email, kept in the daemon's memory (never
// in the operator's config) until a day passes or the key, account or
// email changes.
type foundUser struct {
	account, email, stamp string
	user                  string
	missing               bool
	at                    time.Time
}

// userLookupTTL is how long a found user is kept.
const userLookupTTL = 24 * time.Hour

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

// Scan takes one reading. No key on the machine is not a failure: it is
// Fireworks not being used here.
func (p *Poller) Scan(ctx context.Context) {
	now := p.opts.Now().UTC()
	r, ok := p.read(ctx)
	if !ok {
		return
	}
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

// read takes one reading and records the poll's health; ok is false when
// there is nothing to store.
func (p *Poller) read(ctx context.Context) (Reading, bool) {
	now := p.opts.Now().UTC()
	account, user := Identity(p.opts.Home)
	reason := ""
	switch {
	case p.opts.UserID != "":
		user = p.opts.UserID
	case user == "" && p.opts.Finder != nil:
		account, user, reason = p.findUser(ctx, account, now)
	}
	r, err := p.client.Read(ctx, account, user, now)
	if errors.Is(err, ErrNoKey) {
		p.opts.Logger.Debug("fireworks: no API key on this machine; idle")
		return Reading{}, false
	}
	if err != nil {
		p.opts.Health.Failed(err, now)
		p.opts.Logger.Warn("fireworks: reading failed", "err", err)
		return Reading{}, false
	}
	p.opts.Health.Succeeded(now)
	if r.Scope == ScopeAccount && r.Fallback == "" {
		r.Fallback = reason
		if r.Fallback == "" && user == "" {
			r.Fallback = FallbackNoUser
		}
	}
	return r, true
}

// findUser finds the user by the sign-in email, from memory while that is
// fresh. It returns the account (resolved from the key when the files name
// none), the user, and why there is none.
func (p *Poller) findUser(ctx context.Context, account string, now time.Time) (string, string, string) {
	email := strings.TrimSpace(p.opts.Email)
	if email == "" {
		email = SignInEmail(p.opts.Home)
	}
	if email == "" {
		return account, "", FallbackNoUser
	}
	stamp, err := p.opts.Finder.KeyStamp(ctx)
	if err != nil {
		// No key: Read reports it the same way.
		return account, "", ""
	}
	p.mu.Lock()
	f := p.found
	p.mu.Unlock()
	if f.email == email && f.stamp == stamp && (account == "" || account == f.account) && now.Sub(f.at) < userLookupTTL {
		if f.missing {
			return f.account, "", FallbackNoMatch
		}
		return f.account, f.user, ""
	}
	resolved, user, err := p.opts.Finder.FindUser(ctx, account, email)
	switch {
	case errors.Is(err, ErrNoUserMatch):
		p.remember(foundUser{account: resolved, email: email, stamp: stamp, missing: true, at: now})
		return resolved, "", FallbackNoMatch
	case err != nil:
		// Not kept: the next poll tries again.
		p.opts.Logger.Warn("fireworks: user lookup failed", "status", StatusOf(err))
		if resolved == "" {
			resolved = account
		}
		return resolved, "", FallbackLookupFailed(err)
	}
	p.remember(foundUser{account: resolved, email: email, stamp: stamp, user: user, at: now})
	return resolved, user, ""
}

func (p *Poller) remember(f foundUser) {
	p.mu.Lock()
	p.found = f
	p.mu.Unlock()
}

// NewEnvelope stores a reading in the shape the Claude usage meter uses
// for a spend limit (extra_usage_*), so headroom reads both the same way.
// billing=per_token marks an account billed per token, which headroom may
// bind to pay-as-you-go when no plan is bound.
func NewEnvelope(ts time.Time, r Reading) *eventschema.Envelope {
	h := sha256.Sum256([]byte("fireworks-usage|" + r.AccountID + "|" + strconv.FormatInt(ts.UnixNano(), 10)))
	env := &eventschema.Envelope{
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
	if !r.BlockedUntil.IsZero() {
		env.Attributes["extra_usage_blocked_until"] = r.BlockedUntil.UTC().Format(time.RFC3339)
	}
	if r.Fallback != "" {
		env.Attributes["scope_reason"] = r.Fallback
	}
	return env
}
