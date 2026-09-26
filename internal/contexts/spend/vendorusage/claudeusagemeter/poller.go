package claudeusagemeter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller. signal_quality
// promotes Anthropic confidence to HIGH on observation with an
// explicit ToS-grey caveat.
const SourceTag = "claude-usage-meter"

// PollerOptions configures the periodic /usage poll.
type PollerOptions struct {
	// Health, when set, receives this poller's own success/failure
	// record. It is what lets a status surface tell a reader being
	// refused apart from a vendor nobody is using — both produce no
	// events. nil disables the reporting.
	Health     *freshness.Recorder
	SessionKey string
	Clearance  string
	UserAgent  string
	OrgID      string        // empty → resolved via /api/organizations on first scan
	Interval   time.Duration // defaults 5 minutes
	BaseURL    string        // test override
	Logger     *slog.Logger
	// Cookies, when set, re-reads the claude.ai session and Cloudflare
	// clearance from the operator's browser. The clearance cookie expires
	// within hours and is bound to the browser's address, so a meter that
	// only ever read it once works today and is refused tomorrow. Called
	// at startup and again whenever a request is refused.
	Cookies func(ctx context.Context) (Session, error)
}

// Session is what a browser holds for claude.ai: the login, the proof it
// passed the bot check, and the agent string that proof is bound to.
type Session struct {
	Key       string
	Clearance string
	UserAgent string
	// Browser names where it came from, for logs.
	Browser string
}

// Poller wraps the cookie-auth claude.ai client with a tick loop and
// envelope emission. Each reading that differs from the last one published
// becomes its own event; the newest is what headroom reads.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu          sync.Mutex
	client      *Client
	orgID       string
	publishes   int64
	lastErr     error
	lastErrTime time.Time
	// lastPublished is the attribute set of the last reading published,
	// so an unchanged reading is not stored again.
	lastPublished string
	// lastUnrecognised is the last set of unreadable blocks reported, so
	// the warning is logged when it changes rather than every poll.
	lastUnrecognised string
}

func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{bus: bus, opts: opts, orgID: opts.OrgID}
}

func (p *Poller) Run(ctx context.Context) error {
	if err := p.ensureClient(); err != nil {
		return err
	}
	t := time.NewTicker(p.opts.Interval)
	defer t.Stop()
	p.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.scan(ctx)
		}
	}
}

// PublishCount reports envelopes emitted since boot.
func (p *Poller) PublishCount() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishes
}

// LastError exposes the most recent scan error.
func (p *Poller) LastError() (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErrTime, p.lastErr
}

func (p *Poller) ensureClient() error {
	if p.opts.SessionKey == "" && p.opts.Cookies == nil {
		return ErrMissingCookie
	}
	c := NewClient(p.opts.SessionKey)
	c.Clearance, c.UserAgent = p.opts.Clearance, p.opts.UserAgent
	if p.opts.BaseURL != "" {
		c.BaseURL = p.opts.BaseURL
	}
	p.client = c
	return nil
}

// refreshCookies re-reads the session and clearance from the browser. It is
// how the meter keeps working: the clearance cookie expires within hours,
// and the alternative is an operator re-running setup every morning.
func (p *Poller) refreshCookies(ctx context.Context) bool {
	if p.opts.Cookies == nil || p.client == nil {
		return false
	}
	s, err := p.opts.Cookies(ctx)
	if err != nil || s.Key == "" {
		if err != nil {
			p.opts.Logger.Warn("claude-usage-meter: could not re-read the browser session", "err", err)
		}
		return false
	}
	changed := s.Key != p.client.SessionKey || s.Clearance != p.client.Clearance
	p.client.SessionKey, p.client.Clearance, p.client.UserAgent = s.Key, s.Clearance, s.UserAgent
	if changed {
		p.opts.Logger.Info("claude-usage-meter: refreshed the session from the browser", "browser", s.Browser)
	}
	return changed
}

// refusedRequest reports whether err is the kind a fresh cookie fixes.
func refusedRequest(err error) bool {
	return errors.Is(err, ErrBotCheck) || errors.Is(err, ErrUnauthorized)
}

func (p *Poller) scan(ctx context.Context) {
	if p.client == nil {
		if err := p.ensureClient(); err != nil {
			p.recordErr(err)
			if errors.Is(err, ErrMissingCookie) {
				p.opts.Logger.Debug("claude-usage-meter: session_key missing; idle")
				return
			}
			p.opts.Logger.Warn("claude-usage-meter: client init failed", "err", err)
			return
		}
	}
	if p.client.SessionKey == "" {
		p.refreshCookies(ctx)
	}
	if p.orgID == "" {
		orgID, err := p.resolveOrg(ctx)
		if err != nil && refusedRequest(err) && p.refreshCookies(ctx) {
			orgID, err = p.resolveOrg(ctx)
		}
		if err != nil {
			p.recordErr(err)
			if errors.Is(err, ErrUnauthorized) {
				p.opts.Logger.Warn("claude-usage-meter: cookie expired, re-paste from devtools", "err", err)
				return
			}
			p.opts.Logger.Warn("claude-usage-meter: organizations lookup failed", "err", err)
			return
		}
		p.orgID = orgID
	}
	usage, err := p.client.Usage(ctx, p.orgID)
	if err != nil && refusedRequest(err) && p.refreshCookies(ctx) {
		// The browser had a fresher session; one retry rather than
		// waiting a whole interval to use it.
		usage, err = p.client.Usage(ctx, p.orgID)
	}
	if err != nil {
		p.recordErr(err)
		if errors.Is(err, ErrBotCheck) {
			p.opts.Logger.Warn("claude-usage-meter: bot check refused the request; open claude.ai in your browser once to renew it", "err", err)
			return
		}
		if errors.Is(err, ErrUnauthorized) {
			p.opts.Logger.Warn("claude-usage-meter: cookie expired, re-paste from devtools", "err", err)
			return
		}
		p.opts.Logger.Warn("claude-usage-meter: Usage() failed", "err", err)
		return
	}
	p.recordSuccess()
	p.reportUnrecognised(usage.Unrecognised)
	if !usage.HasSignal() {
		p.opts.Logger.Debug("claude-usage-meter: Anthropic reports nothing to meter for this org")
		return
	}
	env := newEnvelope(time.Now().UTC(), p.orgID, usage)
	key := attrsKey(env.Attributes)
	p.mu.Lock()
	if key == p.lastPublished {
		p.mu.Unlock()
		return
	}
	p.lastPublished = key
	p.mu.Unlock()
	if p.bus != nil {
		p.publishWait(ctx, env)
	}
}

// resolveOrg picks the organization to meter when config names none. An
// account can belong to several — a Claude Enterprise org and a personal
// chat-only one, say — and only some carry usage. The first with anything
// to meter wins; with none, the first listed.
func (p *Poller) resolveOrg(ctx context.Context) (string, error) {
	orgs, err := p.client.Organizations(ctx)
	if err != nil {
		return "", err
	}
	if len(orgs) == 0 {
		return "", errors.New("no organizations returned for sessionKey")
	}
	var refused error
	for _, o := range orgs {
		u, err := p.client.Usage(ctx, o.UUID)
		if err != nil {
			if refusedRequest(err) {
				refused = err
			}
			continue
		}
		if u.HasSignal() {
			p.opts.Logger.Info("claude-usage-meter: resolved org_id", "org_id", o.UUID)
			return o.UUID, nil
		}
	}
	if refused != nil {
		return "", refused
	}
	p.opts.Logger.Info("claude-usage-meter: resolved org_id; no organization reports usage yet", "org_id", orgs[0].UUID)
	return orgs[0].UUID, nil
}

// reportUnrecognised warns when Anthropic returned a block this meter cannot
// read. Those blocks are dropped rather than read as zeros, so without this
// the meter would go quiet with no reason given.
func (p *Poller) reportUnrecognised(names []string) {
	key := strings.Join(names, ",")
	p.mu.Lock()
	changed := key != p.lastUnrecognised
	p.lastUnrecognised = key
	p.mu.Unlock()
	if changed && key != "" {
		p.opts.Logger.Warn("claude-usage-meter: Anthropic returned usage in a shape this version cannot read; "+
			"those readings are skipped, not reported as zero", "blocks", key)
	}
}

func attrsKey(attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + attrs[k] + ";")
	}
	return b.String()
}

func (p *Poller) recordErr(err error) {
	p.opts.Health.Failed(err, time.Now())
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = err
	p.lastErrTime = time.Now()
}

func (p *Poller) recordSuccess() {
	p.opts.Health.Succeeded(time.Now())
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = nil
}

// newEnvelope serialises one usage reading into a PromptEvent envelope.
// Only blocks Anthropic reported are written: a window it did not return
// has no attribute, so headroom falls back to its estimate instead of
// reading a 0% nobody measured. Payload token counts stay zero — this is a
// quota-state snapshot, not a per-turn record.
//
// The ID is per reading. It used to be derived from the five-hour reset
// time, and the store keeps the first event per ID, so only the first poll
// after each reset was ever stored — and on Enterprise, with no window, the
// first reading ever.
func newEnvelope(ts time.Time, orgID string, u *UsageResponse) *eventschema.Envelope {
	h := sha256.Sum256([]byte("claude-usage-meter|" + orgID + "|" + strconv.FormatInt(ts.UnixNano(), 10)))
	attrs := map[string]string{"org_id": orgID}
	for label, window := range u.Windows {
		attrs[label+"_used_pct"] = fmt.Sprintf("%.2f", *window.Utilization)
		if window.ResetsAt != "" {
			attrs[label+"_reset_at"] = window.ResetsAt
		}
		if window.Kind != "" {
			attrs[label+"_kind"] = window.Kind
		}
		if window.ModelScope != "" {
			attrs[label+"_model_scope"] = window.ModelScope
		}
		if window.SurfaceScope != "" {
			attrs[label+"_surface_scope"] = window.SurfaceScope
		}
	}
	if e := u.ExtraUsage; e != nil {
		used, limit := e.Amounts()
		attrs["extra_usage_used"] = fmt.Sprintf("%.2f", used)
		attrs["extra_usage_limit"] = fmt.Sprintf("%.2f", limit)
		attrs["extra_usage_currency"] = e.Currency
		attrs["extra_usage_limit_reached"] = strconv.FormatBool(e.SpendLimitReached)
	}
	return &eventschema.Envelope{
		ID:            "ack-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes:    attrs,
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic,
			Status:   200,
		},
	}
}

// publishWait hands env to the bus and waits for room rather than letting
// it be dropped. Ingestion marks each row seen before publishing, so a
// dropped envelope is never revisited: the loss is permanent, silent, and
// reproduces identically on every restart. Waiting costs a backfill some
// wall-clock and costs the operator nothing.
func (p *Poller) publishWait(ctx context.Context, env *eventschema.Envelope) {
	if env == nil {
		return
	}
	if err := p.bus.PublishWait(ctx, env); err != nil {
		p.opts.Logger.Warn("usage event not stored", "err", err)
		return
	}
	p.mu.Lock()
	p.publishes++
	p.mu.Unlock()
}
