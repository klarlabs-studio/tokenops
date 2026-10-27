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
	// SignIn asks Nexus CLI who is signed in, before its files are
	// scanned for an email; nil skips it. The daemon runs
	// `nexus status --json`.
	SignIn SignInSource
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
	// email (the configured one), stamp and fileAccount are what the
	// lookup was made with; account and user what it found.
	email, stamp, fileAccount string
	account, user             string
	missing                   bool
	at                        time.Time
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
	} else if r.Scope == ScopeAccount && p.opts.UserID != "" {
		// The operator named the user; a failure is theirs to fix.
		r.Fallback += " — check `tokenops vendor-usage enable fireworks --user-id <id>`"
	}
	return r, true
}

// findUser finds the user when no file names them, from memory while
// that is fresh. Sources, in order: the configured email; Nexus CLI's
// status (a user it names is used as is, an email it names is looked
// up); the email in Nexus CLI's files. It returns the account (resolved
// from the key when nothing names it), the user, and why there is none.
func (p *Poller) findUser(ctx context.Context, account string, now time.Time) (string, string, string) {
	stamp, err := p.opts.Finder.KeyStamp(ctx)
	if err != nil {
		// No key: Read reports it the same way.
		return account, "", ""
	}
	configured := strings.TrimSpace(p.opts.Email)
	p.mu.Lock()
	f := p.found
	p.mu.Unlock()
	if f.email == configured && f.stamp == stamp && f.fileAccount == account && now.Sub(f.at) < userLookupTTL {
		if f.missing {
			return f.account, "", FallbackNoMatch
		}
		return f.account, f.user, ""
	}
	keep := foundUser{email: configured, stamp: stamp, fileAccount: account, at: now}
	email := configured
	if email == "" {
		for _, source := range p.signInSources() {
			in := source(ctx)
			if account == "" {
				account = in.Account
			}
			if in.User != "" {
				keep.account, keep.user = account, in.User
				p.remember(keep)
				return account, in.User, ""
			}
			if in.Email != "" {
				email = in.Email
				break
			}
		}
	}
	if email == "" {
		return account, "", FallbackNoUser
	}
	resolved, user, err := p.opts.Finder.FindUser(ctx, account, email)
	switch {
	case errors.Is(err, ErrNoUserMatch):
		keep.account, keep.missing = resolved, true
		p.remember(keep)
		return resolved, "", FallbackNoMatch
	case err != nil:
		// Not kept: the next poll tries again.
		p.opts.Logger.Warn("fireworks: user lookup failed", "status", StatusOf(err))
		if resolved == "" {
			resolved = account
		}
		return resolved, "", FallbackLookupFailed(err)
	}
	keep.account, keep.user = resolved, user
	p.remember(keep)
	return resolved, user, ""
}

// signInSources are where the signed-in user is looked for, first first:
// Nexus CLI's status, then the email in its files.
func (p *Poller) signInSources() []SignInSource {
	var out []SignInSource
	if p.opts.SignIn != nil {
		out = append(out, p.opts.SignIn)
	}
	return append(out, func(context.Context) SignIn { return SignIn{Email: SignInEmail(p.opts.Home)} })
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
