package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pollnow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller. signal_quality
// promotes to HIGH on observation with an explicit caveat that the
// underlying endpoint is undocumented.
const SourceTag = "cursor-web"

// PollerOptions configures the periodic read of cursor.com's usage.
type PollerOptions struct {
	// Health, when set, receives this poller's own success/failure
	// record. It is what lets a status surface tell a reader being
	// refused apart from a vendor nobody is using — both produce no
	// events. nil disables the reporting.
	Health   *freshness.Recorder
	Cookie   string
	UserID   string
	Interval time.Duration
	Logger   *slog.Logger
	// NewClient binds the cookie and user ID to a UsageSource. Required;
	// the daemon passes the HTTP client from
	// internal/infra/vendorusage/cursor.
	NewClient func(cookie, userID string) UsageSource
	// Now is the clock a reading is stamped with; nil is time.Now.
	Now func() time.Time
}

// Poller fetches one usage snapshot per tick and stores one reading of
// it: the weekly pool, the billing cycle, or, from the legacy endpoint,
// the request allowance. A reading that differs from the last one stored
// is stored, and an unchanged one again every heartbeat.Every, so the
// newest reading is never old while the poller works.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu          sync.Mutex
	client      UsageSource
	publishes   int64
	lastErr     error
	lastErrTime time.Time
	// kept is the last reading stored and when.
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

func (p *Poller) Run(ctx context.Context) error {
	if err := p.ensureClient(); err != nil {
		return err
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

// LastError exposes the most recent scan error.
func (p *Poller) LastError() (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErrTime, p.lastErr
}

// errNoClientFactory reports a poller wired without PollerOptions.NewClient.
var errNoClientFactory = errors.New("cursor poller: no client factory configured")

func (p *Poller) ensureClient() error {
	if p.opts.Cookie == "" || p.opts.UserID == "" {
		return ErrMissingCredential
	}
	if p.opts.NewClient == nil {
		return errNoClientFactory
	}
	p.client = p.opts.NewClient(p.opts.Cookie, p.opts.UserID)
	return nil
}

func (p *Poller) scan(ctx context.Context) {
	if p.client == nil {
		if err := p.ensureClient(); err != nil {
			p.recordErr(err)
			if errors.Is(err, ErrMissingCredential) {
				p.opts.Logger.Debug("cursor poller: credentials missing; idle")
				return
			}
			p.opts.Logger.Warn("cursor poller: client init failed", "err", err)
			return
		}
	}
	resp, err := p.client.Usage(ctx)
	if err != nil {
		p.recordErr(err)
		if errors.Is(err, ErrMissingCredential) {
			return
		}
		p.opts.Logger.Warn("cursor poller: Usage() failed", "err", err)
		return
	}
	p.recordSuccess()
	if p.bus == nil {
		return
	}
	// Cursor names no time for its figures, so the reading's content says
	// whether it changed.
	now := p.opts.Now().UTC()
	key := snapshotKey(resp)
	if !p.kept.Due(key, now) {
		return
	}
	var env *eventschema.Envelope
	switch {
	case resp.Pool != nil:
		env = poolEnvelope(now, p.opts.UserID, resp)
	case resp.Summary != nil:
		env = summaryEnvelope(now, p.opts.UserID, resp.Summary)
	default:
		env = requestsEnvelope(now, p.opts.UserID, resp)
	}
	if env == nil {
		return
	}
	p.kept.Stamp(env.Attributes, key, p.opts.Interval)
	// Remembered once the store has taken it: remembered before, a reading
	// the store refused was not stored until the figures changed.
	if p.publishWait(ctx, env) {
		p.kept.Stored(key, now)
	}
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

// snapshotKey builds a stable string that changes only when the
// observed numbers do: what says whether a reading differs from the last
// one stored.
func snapshotKey(resp *UsageResponse) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "som=%s|", resp.StartOfMonth)
	if pl := resp.Pool; pl != nil {
		_, _ = fmt.Fprintf(h, "pool=%.4f/%s|", pl.UsedPct, pl.ResetsAt.Format(time.RFC3339))
	}
	if s := resp.Summary; s != nil {
		r, _ := s.Read()
		_, _ = fmt.Fprintf(h, "summary=%s|%.4f/%.0f/%.0f/%t|", s.BillingCycleEnd, r.UsedPct, r.UsedCents, r.LimitCents, r.IsUnlimited)
	}
	for _, k := range modelNames(resp) {
		m := resp.Models[k]
		_, _ = fmt.Fprintf(h, "%s=%d/%d|", k, m.NumRequests, m.MaxRequestUsage)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// modelNames is the legacy answer's rows by name, in order: a map has
// none of its own.
func modelNames(resp *UsageResponse) []string {
	names := make([]string, 0, len(resp.Models))
	for name := range resp.Models {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// requestsEnvelope is the legacy answer's reading: the request allowance
// of the plan, which is the row with an allowance, and the busiest when
// several have one. It is nil when no row has an allowance: such an answer
// says nothing about the plan.
//
// Each row used to be stored as an event of its own carrying the row's
// name as its model, so every changed count was counted as a request to
// that model (#549), and the monthly reading took whichever row the store
// returned first.
func requestsEnvelope(ts time.Time, userID string, resp *UsageResponse) *eventschema.Envelope {
	var (
		busiest string
		share   float64
	)
	for _, name := range modelNames(resp) {
		m := resp.Models[name]
		if m.MaxRequestUsage <= 0 {
			continue
		}
		if s := 100 * float64(m.NumRequests) / float64(m.MaxRequestUsage); busiest == "" || s > share {
			busiest, share = name, s
		}
	}
	if busiest == "" {
		return nil
	}
	m := resp.Models[busiest]
	attrs := map[string]string{
		// A monthly usage snapshot, not a user message: plan window math
		// must not count it.
		"granularity":       "monthly_snapshot",
		"user_id":           userID,
		"usage_source":      "usage",
		"allowance_of":      busiest,
		"num_requests":      strconv.Itoa(m.NumRequests),
		"max_request_usage": strconv.Itoa(m.MaxRequestUsage),
		"used_pct":          fmt.Sprintf("%.2f", share),
	}
	if resp.StartOfMonth != "" {
		attrs["start_of_month"] = resp.StartOfMonth
	}
	return snapshotEnvelope(ts, userID, "requests", attrs)
}

// summaryEnvelope is the billing cycle's reading from usage-summary: the
// share used (used_pct, which the monthly headroom reads, with the cycle's
// end as its reset), whose allowance it is, and the cents behind it.
func summaryEnvelope(ts time.Time, userID string, s *Summary) *eventschema.Envelope {
	r, _ := s.Read()
	attrs := map[string]string{
		"granularity":       "monthly_snapshot",
		"user_id":           userID,
		"usage_source":      "usage-summary",
		"membership_type":   s.MembershipType,
		"billing_cycle_end": s.BillingCycleEnd,
	}
	if s.BillingCycleStart != "" {
		attrs["start_of_month"] = s.BillingCycleStart
	}
	if r.HasUsedPct {
		attrs["used_pct"] = fmt.Sprintf("%.2f", r.UsedPct)
		attrs["usage_scope"] = r.Scope
	}
	if r.HasCents {
		attrs["used_cents"] = strconv.FormatFloat(r.UsedCents, 'f', -1, 64)
		attrs["limit_cents"] = strconv.FormatFloat(r.LimitCents, 'f', -1, 64)
	}
	if r.IsUnlimited {
		attrs["unlimited"] = "true"
	}
	return snapshotEnvelope(ts, userID, "plan", attrs)
}

// poolEnvelope is the weekly pool, written as an account reader writes a
// window so headroom shows it beside the other vendors' windows. It
// carries no used_pct: the monthly figures usage-summary still reports
// for these accounts are stale.
func poolEnvelope(ts time.Time, userID string, resp *UsageResponse) *eventschema.Envelope {
	pl := resp.Pool
	attrs := map[string]string{
		"granularity":           "quota_snapshot",
		"billing":               "subscription",
		"user_id":               userID,
		"usage_source":          "get-sand-usage-status",
		"window_0_name":         "week",
		"window_0_used_pct":     fmt.Sprintf("%.2f", pl.UsedPct),
		"window_0_duration_min": strconv.Itoa(int(7 * 24 * time.Hour / time.Minute)),
		"window_0_reset_at":     pl.ResetsAt.UTC().Format(time.RFC3339),
	}
	if pl.PlanName != "" {
		attrs["plan_name"] = pl.PlanName
	}
	if resp.Summary != nil {
		attrs["membership_type"] = resp.Summary.MembershipType
	}
	return snapshotEnvelope(ts, userID, "pool", attrs)
}

// snapshotEnvelope is a reading of one kind taken at ts: no model and no
// tokens, so usage queries never count it as a request (#549).
//
// Its ID is made from the time it was taken. Made from the figures, as it
// was, a reading stored again was the same event to the store, which keeps
// the first of each ID: the figures of an idle account kept the time they
// were first seen, and a reading that returned to an earlier value was
// never stored at all.
func snapshotEnvelope(ts time.Time, userID, kind string, attrs map[string]string) *eventschema.Envelope {
	h := sha256.Sum256([]byte("cursor-web|" + userID + "|" + kind + "|" + strconv.FormatInt(ts.UnixNano(), 10)))
	return &eventschema.Envelope{
		ID:            "cur-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes:    attrs,
		Payload:       &eventschema.PromptEvent{Provider: eventschema.ProviderCursor, Status: 200},
	}
}

// publishWait hands env to the bus, waiting for room rather than letting
// it be dropped, and reports whether the bus took it. A reading it did not
// take is not remembered as stored, so the next poll offers it again.
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
