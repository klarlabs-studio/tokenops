package copilot

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

// SourceTag identifies envelopes emitted by this poller. signal_quality
// classifier upgrades GitHub Copilot confidence to HIGH on observation.
const SourceTag = "github-copilot"

// PollerOptions configures the periodic call to /copilot_internal/user.
type PollerOptions struct {
	// Health, when set, receives this poller's own success/failure
	// record. It is what lets a status surface tell a reader being
	// refused apart from a vendor nobody is using — both produce no
	// events. nil disables the reporting.
	Health *freshness.Recorder
	// OAuthToken can be set explicitly (env / config); empty means
	// "load from disk via TokenPaths".
	OAuthToken string
	// TokenPaths overrides the default discovery locations. Empty
	// uses DefaultTokenPaths.
	TokenPaths []string
	// TokenSources are tried, in order, after the apps.json files when
	// no token was found there: the Copilot CLI's environment variables,
	// its Keychain item and plaintext config, the GitHub CLI. The daemon
	// passes the adapters from internal/infra/vendorusage/copilot.
	TokenSources []TokenSource
	// Interval defaults to 2 minutes. GitHub doesn't document a rate
	// limit on this endpoint but the IDE plugins poll on similar
	// cadence.
	Interval time.Duration
	// Logger required.
	Logger *slog.Logger
	// NewClient binds an OAuth token to a UserSource. Required; the
	// daemon passes the HTTP client from internal/infra/vendorusage/copilot.
	NewClient func(token string) UserSource
}

// Poller queries /copilot_internal/user on a tick and emits one
// PromptEvent envelope per quota snapshot (one for `chat`, one for
// `premium_interactions`, etc.). Envelope IDs are deterministic per
// (timestamp_utc, snapshot key) so re-running never double-counts
// against the store.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu          sync.Mutex
	client      UserSource
	publishes   int64
	lastErr     error
	lastErrTime time.Time
	lastSeenTs  string // timestamp_utc from the last successful response — dedup key
}

func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{bus: bus, opts: opts}
}

// Run polls until ctx ends. A token not found at start does not end it:
// each scan looks again and records why, so the operator who signs in
// later is read without a restart, and a status surface says what is
// missing rather than nothing at all.
func (p *Poller) Run(ctx context.Context) error {
	if p.opts.NewClient == nil {
		return errNoClientFactory
	}
	t := pollnow.NewTicker(ctx, p.opts.Interval)
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

// LastError exposes the most recent scan error + when it happened.
// Returned with time first so revive's error-must-be-last lint stays
// happy.
func (p *Poller) LastError() (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErrTime, p.lastErr
}

// errNoClientFactory reports a poller wired without PollerOptions.NewClient.
var errNoClientFactory = errors.New("copilot poller: no client factory configured")

func (p *Poller) ensureClient(ctx context.Context) error {
	token := p.opts.OAuthToken
	if token == "" {
		paths := p.opts.TokenPaths
		if len(paths) == 0 {
			defaults, err := DefaultTokenPaths()
			if err != nil {
				return err
			}
			paths = defaults
		}
		sources := append([]TokenSource{FileSource{Paths: paths}}, p.opts.TokenSources...)
		t, err := FirstToken(ctx, sources)
		if err != nil {
			return err
		}
		token = t
	}
	if p.opts.NewClient == nil {
		return errNoClientFactory
	}
	p.client = p.opts.NewClient(token)
	return nil
}

func (p *Poller) scan(ctx context.Context) {
	if p.client == nil {
		if err := p.ensureClient(ctx); err != nil {
			p.recordErr(err)
			if errors.Is(err, ErrNoToken) {
				p.opts.Logger.Debug("copilot poller: no oauth token available; idle")
				return
			}
			p.opts.Logger.Warn("copilot poller: client init failed", "err", err)
			return
		}
	}
	resp, err := p.client.User(ctx)
	if err != nil {
		p.recordErr(err)
		if errors.Is(err, ErrNoToken) {
			return
		}
		p.opts.Logger.Warn("copilot poller: User() failed", "err", err)
		return
	}
	reading, err := resp.Reading()
	if err != nil {
		p.recordErr(err)
		p.opts.Logger.Warn("copilot poller: unreadable user record", "err", err)
		return
	}
	p.recordSuccess()
	// Dedup the whole poll against the response's timestamp_utc —
	// snapshots refresh slowly server-side and re-querying inside the
	// same minute returns identical data.
	p.mu.Lock()
	if resp.TimestampUTC != "" && resp.TimestampUTC == p.lastSeenTs {
		p.mu.Unlock()
		return
	}
	if resp.TimestampUTC != "" {
		p.lastSeenTs = resp.TimestampUTC
	}
	p.mu.Unlock()
	ts := time.Now().UTC()
	if resp.TimestampUTC != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, resp.TimestampUTC); err == nil {
			ts = parsed
		}
	}
	if p.bus == nil {
		return
	}
	for key, snap := range resp.QuotaSnapshots {
		p.publishWait(ctx, newEnvelope(ts, resp, key, snap))
	}
	p.publishWait(ctx, newPlanEnvelope(ts, resp, reading))
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

// newEnvelope wraps one quota snapshot as a PromptEvent envelope.
// Copilot doesn't emit per-turn token counts; the response is a
// rolling quota state. We encode it as a zero-token envelope with
// the live percent_remaining + entitlement in Attributes so the
// signal_quality classifier and future Copilot-aware session_budget
// can read it.
func newEnvelope(ts time.Time, resp *UserResponse, snapshotKey string, snap QuotaSnapshot) *eventschema.Envelope {
	h := sha256.Sum256([]byte("github-copilot|" + resp.Login + "|" + resp.TimestampUTC + "|" + snapshotKey))
	env := &eventschema.Envelope{
		ID:            "ghc-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes: map[string]string{
			// Quota-state snapshot, not a user message — plan window
			// math must not count it (plans.ConsumptionInWindow).
			"granularity":      "quota_snapshot",
			"login":            resp.Login,
			"snapshot":         snapshotKey,
			"remaining":        fmt.Sprintf("%.2f", snap.Remaining),
			"entitlement":      fmt.Sprintf("%d", snap.Entitlement),
			"overage_count":    fmt.Sprintf("%d", snap.OverageCount),
			"unlimited":        fmt.Sprintf("%t", snap.Unlimited),
			"quota_reset_date": resp.QuotaResetDate,
			"chat_enabled":     fmt.Sprintf("%t", resp.ChatEnabled),
			"credit_billed":    fmt.Sprintf("%t", resp.TokenBasedBilling || snap.TokenBasedBilling),
		},
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderGitHub,
			Status:   200,
		},
	}
	// A bucket that does not report its share carries no
	// percent_remaining: absent is not "0% left".
	if snap.PercentRemaining != nil {
		env.Attributes["percent_remaining"] = fmt.Sprintf("%.2f", *snap.PercentRemaining)
	}
	if snap.CreditsUsed != nil {
		env.Attributes["credits_used"] = strconv.FormatFloat(*snap.CreditsUsed, 'f', -1, 64)
	}
	return env
}

// PlanSnapshot is the snapshot attribute of the one envelope per poll that
// carries the plan as a whole: its capped allowances as windows, the AI
// Credits used, and whether the seat is metered at all.
const PlanSnapshot = "plan"

// newPlanEnvelope stores the poll's Reading in the shape vendor account
// readers use (window_<n>_{name,used_pct,reset_at}, used_credits), so
// headroom shows Copilot's month as a window like any other vendor's.
// seat is "metered" or "unmetered": an unmetered seat (an organisation
// seat with no personal allowance) has no window, and headroom says so
// instead of reading it as unlimited headroom.
func newPlanEnvelope(ts time.Time, resp *UserResponse, r Reading) *eventschema.Envelope {
	h := sha256.Sum256([]byte("github-copilot|" + resp.Login + "|" + resp.TimestampUTC + "|" + PlanSnapshot))
	attrs := map[string]string{
		"granularity": "quota_snapshot",
		"snapshot":    PlanSnapshot,
		"seat":        "metered",
	}
	if r.Unmetered {
		attrs["seat"] = "unmetered"
	}
	if resp.CopilotPlan != "" {
		attrs["plan_type"] = resp.CopilotPlan
	}
	if r.HasCredits {
		attrs["used_credits"] = strconv.FormatFloat(r.CreditsUsed, 'f', -1, 64)
		attrs["used_credits_unit"] = CreditsUnit
	}
	for i, m := range r.Meters {
		k := "window_" + strconv.Itoa(i) + "_"
		attrs[k+"name"] = m.Name
		attrs[k+"used_pct"] = fmt.Sprintf("%.2f", m.UsedPct)
		if !r.ResetsAt.IsZero() {
			attrs[k+"reset_at"] = r.ResetsAt.Format(time.RFC3339)
		}
	}
	return &eventschema.Envelope{
		ID:            "ghc-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes:    attrs,
		Payload:       &eventschema.PromptEvent{Provider: eventschema.ProviderGitHub, Status: 200},
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
