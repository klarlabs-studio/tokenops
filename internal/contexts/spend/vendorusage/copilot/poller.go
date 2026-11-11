package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
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
	// Now is the clock a reading is stamped with; nil is time.Now.
	Now func() time.Time
}

// Poller queries /copilot_internal/user on a tick and stores one reading
// per quota snapshot (`chat`, `premium_interactions`, …) and one of the
// plan as a whole. A poll's readings carry one time, the poll's: that is
// what makes them one set to whoever reads them back. A set that differs
// from the last one stored is stored, and an unchanged one again every
// heartbeat.Every, so the newest set is never old while the poller works.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu          sync.Mutex
	client      UserSource
	publishes   int64
	lastErr     error
	lastErrTime time.Time
	// kept is the last set of readings stored and when.
	kept heartbeat.Keeper
}

func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
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
	if p.bus == nil {
		return
	}
	now := p.opts.Now().UTC()
	envs := make([]*eventschema.Envelope, 0, len(resp.QuotaSnapshots)+1)
	for _, name := range snapshotNames(resp) {
		envs = append(envs, newEnvelope(now, resp, name, resp.QuotaSnapshots[name]))
	}
	envs = append(envs, newPlanEnvelope(now, resp, reading))
	// The record's own timestamp_utc is no part of what is compared: were
	// it the time of the answer, every poll would differ from the last.
	key := readingsKey(envs)
	if !p.kept.Due(key, now) {
		return
	}
	stored := true
	for _, env := range envs {
		if resp.TimestampUTC != "" {
			env.Attributes["reported_at"] = resp.TimestampUTC
		}
		heartbeat.Stamp(env.Attributes, p.opts.Interval)
		if !p.publishWait(ctx, env) {
			stored = false
		}
	}
	// Remembered once the store has taken the whole set: a set the store
	// took part of is offered again on the next poll.
	if stored {
		p.kept.Stored(key, now)
	}
}

// snapshotNames is the record's quota snapshots by name, in order: a map
// has none of its own.
func snapshotNames(resp *UserResponse) []string {
	names := make([]string, 0, len(resp.QuotaSnapshots))
	for name := range resp.QuotaSnapshots {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// readingsKey identifies the content of a poll's readings: what says
// whether the set differs from the last one stored.
func readingsKey(envs []*eventschema.Envelope) string {
	var b strings.Builder
	for _, env := range envs {
		names := make([]string, 0, len(env.Attributes))
		for name := range env.Attributes {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			b.WriteString(name + "=" + env.Attributes[name] + ";")
		}
		b.WriteString("|")
	}
	return b.String()
}

func (p *Poller) recordErr(err error) {
	now := p.opts.Now()
	p.opts.Health.Failed(err, now)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = err
	p.lastErrTime = now
}

func (p *Poller) recordSuccess() {
	p.opts.Health.Succeeded(p.opts.Now())
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
	env := &eventschema.Envelope{
		ID:            readingID(ts, resp.Login, snapshotKey),
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
		ID:            readingID(ts, resp.Login, PlanSnapshot),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes:    attrs,
		Payload:       &eventschema.PromptEvent{Provider: eventschema.ProviderGitHub, Status: 200},
	}
}

// readingID is the ID of the reading of one snapshot taken at ts.
//
// It was made from the record's timestamp_utc. The store keeps the first
// event of each ID, so wherever that timestamp stands still while nothing
// is used, a reading stored again would have been the event the store
// already had, under the time it was first seen.
func readingID(ts time.Time, login, snapshot string) string {
	h := sha256.Sum256([]byte("github-copilot|" + login + "|" + strconv.FormatInt(ts.UnixNano(), 10) + "|" + snapshot))
	return "ghc-" + hex.EncodeToString(h[:8])
}

// publishWait hands env to the bus, waiting for room rather than letting
// it be dropped, and reports whether the bus took it. A set with a reading
// it did not take is not remembered as stored, so the next poll offers it
// again.
func (p *Poller) publishWait(ctx context.Context, env *eventschema.Envelope) bool {
	if err := p.bus.PublishWait(ctx, env); err != nil {
		p.opts.Logger.Warn("usage event not stored", "err", err)
		return false
	}
	p.mu.Lock()
	p.publishes++
	p.mu.Unlock()
	return true
}
