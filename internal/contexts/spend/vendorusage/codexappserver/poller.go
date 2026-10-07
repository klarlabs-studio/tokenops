package codexappserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
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

// Scan asks Codex once.
func (p *Poller) Scan(ctx context.Context) {
	snap, err := Read(ctx, p.opts.Dial)
	now := p.opts.Now()
	if err != nil {
		p.opts.Health.Failed(err, now)
		p.opts.Logger.Warn("codex-app-server: rate limits not read", "err", err)
		return
	}
	p.opts.Health.Succeeded(now)
	env := Envelope(snap, now.UTC())
	key := attributesKey(env.Attributes)
	if key == p.lastKey {
		return
	}
	p.lastKey = key
	if p.bus != nil {
		if err := p.bus.PublishWait(ctx, env); err != nil {
			p.opts.Logger.Warn("codex-app-server: reading not stored", "err", err)
		}
	}
}

// Envelope is a snapshot as a reading: no model and no tokens, so usage
// queries never count it as a request (#549).
func Envelope(s Snapshot, at time.Time) *eventschema.Envelope {
	attrs := map[string]string{"granularity": "quota_snapshot", "plan_type": s.PlanType}
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
	h := sha256.Sum256([]byte(SourceTag + "|" + strconv.FormatInt(at.UnixNano(), 10)))
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
