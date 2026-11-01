package codexappserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pollnow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag marks readings from this source.
const SourceTag = "codex-app-server"

// DefaultInterval is how often Codex is asked. Each ask starts a short-
// lived app server and one request to OpenAI, so it is not polled hard.
const DefaultInterval = 15 * time.Minute

// PollerOptions configures the poller.
type PollerOptions struct {
	Dial     Dial
	Interval time.Duration
	Health   *freshness.Recorder
	Logger   *slog.Logger
	Now      func() time.Time
}

// Poller asks Codex for its windows on a schedule and publishes each new
// reading in the attribute shape Codex's rollouts already use.
type Poller struct {
	bus     events.Bus
	opts    PollerOptions
	lastKey string
	// credits is the reset-credit count the last read reported, and
	// details the credits last read in detail, at detailsAt, for that
	// count: kept so a read without details still carries their expiry.
	credits   int64
	details   []ResetCredit
	detailsAt time.Time
}

// creditDetailsEvery is how often the reset credits are read in detail
// while the account has some. Codex fetches them in a lookup of their own;
// credits are granted and expire over days, not minutes.
const creditDetailsEvery = time.Hour

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

// Scan asks Codex once, and stores one reading per limit bucket whose
// windows changed since the last scan.
func (p *Poller) Scan(ctx context.Context) {
	now := p.opts.Now()
	details := p.credits > 0 && now.Sub(p.detailsAt) >= creditDetailsEvery
	buckets, err := ReadAllWith(ctx, p.opts.Dial, ReadOptions{CreditDetails: details})
	if err != nil {
		p.opts.Health.Failed(err, now)
		p.opts.Logger.Warn("codex-app-server: rate limits not read", "err", err)
		return
	}
	p.opts.Health.Succeeded(now)
	p.keepCreditDetails(&buckets[0], details, now)
	envs := make([]*eventschema.Envelope, 0, len(buckets))
	var key strings.Builder
	for _, b := range buckets {
		env := Envelope(b, now.UTC())
		envs = append(envs, env)
		key.WriteString(attributesKey(env.Attributes) + "|")
	}
	if key.String() == p.lastKey {
		return
	}
	p.lastKey = key.String()
	if p.bus == nil {
		return
	}
	for _, env := range envs {
		if err := p.bus.PublishWait(ctx, env); err != nil {
			p.opts.Logger.Warn("codex-app-server: reading not stored", "err", err)
		}
	}
}

// keepCreditDetails remembers the reset credits a detailed read returned,
// and gives a read without details the ones last read, while the count is
// unchanged. A changed count means a credit was granted, redeemed or
// expired: the details are read again on the next scan.
func (p *Poller) keepCreditDetails(def *Snapshot, detailed bool, now time.Time) {
	rc := def.ResetCredits
	if rc == nil {
		p.credits, p.details, p.detailsAt = 0, nil, time.Time{}
		return
	}
	switch {
	case detailed:
		p.details, p.detailsAt = rc.Credits, now
	case rc.AvailableCount != p.credits:
		p.details, p.detailsAt = nil, time.Time{}
	case rc.Credits == nil && p.details != nil:
		rc.Credits = p.details
	}
	p.credits = rc.AvailableCount
}

// Envelope is a snapshot as a reading: no model and no tokens, so usage
// queries never count it as a request (#549). A limit other than the
// default names itself with limit_id, and its windows are its own.
func Envelope(s Snapshot, at time.Time) *eventschema.Envelope {
	attrs := map[string]string{"granularity": "quota_snapshot", "plan_type": s.PlanType}
	if !s.IsDefault() {
		attrs["limit_id"] = s.LimitID
		if s.LimitName != "" {
			attrs["limit_name"] = s.LimitName
		}
	}
	for slot, w := range map[string]*Window{"primary": s.Primary, "secondary": s.Secondary} {
		if w == nil {
			continue
		}
		attrs[slot+"_used_pct"] = fmt.Sprintf("%.2f", w.UsedPercent)
		attrs[slot+"_window_min"] = strconv.FormatInt(w.WindowDurationMins, 10)
		attrs[slot+"_resets_at"] = strconv.FormatInt(w.ResetsAt, 10)
	}
	if s.RateLimitReachedType != "" {
		attrs["limit_reached"] = s.RateLimitReachedType
	}
	if s.ResetCredits != nil {
		maps.Copy(attrs, plans.AllowanceAttributes(int(s.ResetCredits.AvailableCount), allowances(s.ResetCredits.Credits)))
	}
	h := sha256.Sum256([]byte(SourceTag + "|" + strconv.FormatInt(at.UnixNano(), 10) + "|" + s.LimitID))
	return &eventschema.Envelope{
		ID:            "cas-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     at,
		Source:        SourceTag,
		Attributes:    attrs,
		Payload:       &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, Status: 200},
	}
}

// allowances is the reset credits still available, as allowances: one
// redeemed or being redeemed is no longer banked.
func allowances(credits []ResetCredit) []plans.Allowance {
	var out []plans.Allowance
	for _, c := range credits {
		if c.Status != "" && c.Status != "available" {
			continue
		}
		a := plans.Allowance{Kind: plans.AllowanceResetCredit, ID: c.ID}
		if c.Title != nil {
			a.Title = *c.Title
		}
		if c.GrantedAt > 0 {
			a.GrantedAt = time.Unix(c.GrantedAt, 0).UTC()
		}
		if c.ExpiresAt != nil && *c.ExpiresAt > 0 {
			a.ExpiresAt = time.Unix(*c.ExpiresAt, 0).UTC()
		}
		out = append(out, a)
	}
	return out
}

func attributesKey(attrs map[string]string) string {
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
