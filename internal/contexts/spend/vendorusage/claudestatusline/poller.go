// Package claudestatusline ingests the plan limits Claude Code reports to
// its status line.
//
// For a claude.ai subscriber Claude Code reports the 5-hour and weekly
// windows; behind a Claude apps gateway, the spend limit. TokenOps' status
// line leaves each reading in a file (internal/infra/claudelimits) and this
// stores it as a vendor reading, in the shape the claude.ai usage meter
// writes, so headroom reads the vendor's own windows without a claude.ai
// login.
package claudestatusline

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

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag marks readings from this source.
const SourceTag = "claude-code-statusline"

// heartbeat is how often an unchanged reading is stored again, so a
// reader that asks for a fresh reading still finds one while Claude Code
// keeps reporting the same figures.
const heartbeat = 10 * time.Minute

// PollerOptions configures the reader.
type PollerOptions struct {
	// Path is the reading file; empty takes the default.
	Path string
	// Interval between reads. Zero takes one minute.
	Interval time.Duration
	Logger   *slog.Logger
}

// Poller reads the status line's newest reading into the bus.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	lastKey string
	lastAt  time.Time
}

// NewPoller builds the reader.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{bus: bus, opts: opts}
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) error {
	tick := time.NewTicker(p.opts.Interval)
	defer tick.Stop()
	p.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			p.once(ctx)
		}
	}
}

func (p *Poller) once(ctx context.Context) {
	r, ok, err := claudelimits.Read(p.opts.Path)
	if err != nil {
		p.opts.Logger.Debug("claude status line limits unreadable", "err", err)
		return
	}
	if !ok || !r.ObservedAt.After(p.lastAt) {
		return
	}
	env := Envelope(r)
	if env == nil {
		return
	}
	key := attrsKey(env.Attributes)
	if key == p.lastKey && r.ObservedAt.Sub(p.lastAt) < heartbeat {
		return
	}
	if p.bus != nil {
		if err := p.bus.PublishWait(ctx, env); err != nil {
			p.opts.Logger.Warn("claude status line reading not stored", "err", err)
			return
		}
	}
	p.lastKey, p.lastAt = key, r.ObservedAt
}

// Envelope turns a reading into a quota snapshot with the claude.ai usage
// meter's keys: <window>_used_pct and an RFC 3339 <window>_reset_at. A
// spend limit becomes a window too, and, when Claude Code has its dollar
// figures for a monthly limit, the extra_usage spend headroom reads. A
// daily or weekly dollar limit is left as a window: headroom's spend is
// monthly, and calling a weekly limit a monthly one would misstate it.
func Envelope(r claudelimits.Reading) *eventschema.Envelope {
	attrs := map[string]string{}
	for _, w := range []struct {
		label string
		win   *claudelimits.Window
	}{{"five_hour", r.FiveHour}, {"seven_day", r.SevenDay}, {"spend_limit", r.SpendLimit}} {
		if w.win == nil {
			continue
		}
		attrs[w.label+"_used_pct"] = fmt.Sprintf("%.2f", w.win.UsedPct)
		if w.win.ResetsAt > 0 {
			attrs[w.label+"_reset_at"] = time.Unix(w.win.ResetsAt, 0).UTC().Format(time.RFC3339)
		}
	}
	if s := r.SpendLimit; s != nil {
		if s.Period != "" {
			attrs["spend_limit_period"] = s.Period
		}
		if s.Period == "monthly" && s.UsedUSD != nil && s.LimitUSD != nil && *s.LimitUSD > 0 {
			attrs["extra_usage_used"] = fmt.Sprintf("%.2f", *s.UsedUSD)
			attrs["extra_usage_limit"] = fmt.Sprintf("%.2f", *s.LimitUSD)
			attrs["extra_usage_currency"] = "USD"
			attrs["extra_usage_limit_reached"] = strconv.FormatBool(s.UsedPct >= 100)
		}
	}
	if len(attrs) == 0 {
		return nil
	}
	// A snapshot of the account, not a request: it carries no tokens and
	// must never count as a message against a window.
	attrs["granularity"] = "quota_snapshot"
	h := sha256.Sum256([]byte(SourceTag + "|" + strconv.FormatInt(r.ObservedAt.UnixNano(), 10)))
	return &eventschema.Envelope{
		ID:            "ccs-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     r.ObservedAt.UTC(),
		Source:        SourceTag,
		Attributes:    attrs,
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic,
			Status:   200,
		},
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
