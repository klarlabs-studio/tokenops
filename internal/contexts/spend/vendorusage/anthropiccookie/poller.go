package anthropiccookie

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller. signal_quality
// promotes Anthropic confidence to HIGH on observation with an
// explicit ToS-grey caveat.
const SourceTag = "anthropic-cookie"

// PollerOptions configures the periodic /usage poll.
type PollerOptions struct {
	SessionKey string
	OrgID      string        // empty → resolved via /api/organizations on first scan
	Interval   time.Duration // defaults 5 minutes
	BaseURL    string        // test override
	Logger     *slog.Logger
}

// Poller wraps the cookie-auth claude.ai client with a tick loop and
// envelope emission. Dedup is by (org_id, five_hour reset_at) since
// the five-hour window resets are the most granular timestamp the
// API ships; weekly utilization changes inside that bucket are
// rolled into the same envelope (overwrite by deterministic ID).
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu          sync.Mutex
	client      *Client
	orgID       string
	publishes   int64
	lastErr     error
	lastErrTime time.Time
	lastSeenKey string
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
	if p.opts.SessionKey == "" {
		return ErrMissingCookie
	}
	c := NewClient(p.opts.SessionKey)
	if p.opts.BaseURL != "" {
		c.BaseURL = p.opts.BaseURL
	}
	p.client = c
	return nil
}

func (p *Poller) scan(ctx context.Context) {
	if p.client == nil {
		if err := p.ensureClient(); err != nil {
			p.recordErr(err)
			if errors.Is(err, ErrMissingCookie) {
				p.opts.Logger.Debug("anthropic-cookie: session_key missing; idle")
				return
			}
			p.opts.Logger.Warn("anthropic-cookie: client init failed", "err", err)
			return
		}
	}
	if p.orgID == "" {
		orgs, err := p.client.Organizations(ctx)
		if err != nil {
			p.recordErr(err)
			if errors.Is(err, ErrUnauthorized) {
				p.opts.Logger.Warn("anthropic-cookie: cookie expired, re-paste from devtools", "err", err)
				return
			}
			p.opts.Logger.Warn("anthropic-cookie: organizations lookup failed", "err", err)
			return
		}
		if len(orgs) == 0 {
			p.recordErr(fmt.Errorf("no organizations returned for sessionKey"))
			return
		}
		p.orgID = orgs[0].UUID
		p.opts.Logger.Info("anthropic-cookie: resolved org_id", "org_id", p.orgID, "org_name", orgs[0].Name)
	}
	usage, err := p.client.Usage(ctx, p.orgID)
	if err != nil {
		p.recordErr(err)
		if errors.Is(err, ErrUnauthorized) {
			p.opts.Logger.Warn("anthropic-cookie: cookie expired, re-paste from devtools", "err", err)
			return
		}
		p.opts.Logger.Warn("anthropic-cookie: Usage() failed", "err", err)
		return
	}
	p.recordSuccess()
	// Dedup by five_hour.reset_at — the most granular field that
	// changes on a known cadence. Weekly utilization shifts inside
	// the same five-hour bucket overwrite via deterministic envelope
	// ID and the store dedups, so this just saves a publish cycle.
	key := usage.FiveHour.ResetAt
	p.mu.Lock()
	if key != "" && key == p.lastSeenKey {
		p.mu.Unlock()
		return
	}
	p.lastSeenKey = key
	p.mu.Unlock()
	env := newEnvelope(time.Now().UTC(), p.orgID, usage)
	if p.bus != nil {
		p.bus.Publish(env)
		p.mu.Lock()
		p.publishes++
		p.mu.Unlock()
	}
}

func (p *Poller) recordErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = err
	p.lastErrTime = time.Now()
}

func (p *Poller) recordSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = nil
}

// newEnvelope serialises the usage snapshot into a PromptEvent
// envelope. The three utilization percentages + reset timestamps
// live in Attributes so the future Max-aware session_budget MCP
// tool can read them directly. Payload token counts stay zero —
// this is a quota-state snapshot, not a per-turn record.
func newEnvelope(ts time.Time, orgID string, u *UsageResponse) *eventschema.Envelope {
	h := sha256.Sum256([]byte("anthropic-cookie|" + orgID + "|" + u.FiveHour.ResetAt))
	attrs := map[string]string{
		"org_id":                  orgID,
		"five_hour_used_pct":      fmt.Sprintf("%.2f", u.FiveHour.UtilizationPct),
		"five_hour_reset_at":      u.FiveHour.ResetAt,
		"seven_day_used_pct":      fmt.Sprintf("%.2f", u.SevenDay.UtilizationPct),
		"seven_day_reset_at":      u.SevenDay.ResetAt,
		"seven_day_opus_used_pct": fmt.Sprintf("%.2f", u.SevenDayOpus.UtilizationPct),
		"seven_day_opus_reset_at": u.SevenDayOpus.ResetAt,
	}
	if u.ExtraUsage != nil {
		attrs["extra_usage_current"] = fmt.Sprintf("%.2f", u.ExtraUsage.CurrentSpending)
		attrs["extra_usage_budget"] = fmt.Sprintf("%.2f", u.ExtraUsage.BudgetLimit)
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
