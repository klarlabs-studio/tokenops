package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller in the event
// store. Consumers (signal_quality classifier, dashboards, scorecard)
// use it to upgrade Anthropic-provider confidence from "low MCP-ping
// proxy" to "medium real activity from Claude Code's local cache".
const SourceTag = "claude-code-stats-cache"

// PollerOptions configures the cache reader.
type PollerOptions struct {
	// Path is the cache file location. Empty defaults to ~/.claude/stats-cache.json.
	Path string
	// Interval is the gap between reads. Cache is rewritten by Claude
	// Code on activity (not on a clock), so anything between 15s and
	// 5min is reasonable. Defaults to 60s.
	Interval time.Duration
	// Logger is used for non-fatal failures (missing cache, parse
	// errors). Required.
	Logger *slog.Logger
}

// Poller diffs successive cache reads and publishes one PromptEvent
// envelope per (date, model) increment to the configured bus. State is
// kept in-memory: a restart re-scans the latest day so the operator
// loses at most one day of catch-up.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu        sync.Mutex
	lastSeen  map[string]map[string]int64 // date → model → cumulative tokens
	lastBoot  time.Time
	publishes int64
}

// NewPoller binds bus + options. Bus may be nil — in that case the
// poller reads the cache and counts diffs but emits no envelopes;
// useful for status commands that only want to display the current
// state.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 60 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{
		bus:      bus,
		opts:     opts,
		lastSeen: make(map[string]map[string]int64),
		lastBoot: time.Now(),
	}
}

// Run blocks until ctx is cancelled, reading the cache on every tick
// and emitting envelopes for new traffic. Returns ctx.Err() on
// shutdown; never propagates read errors so a transient permission
// failure doesn't kill the loop.
func (p *Poller) Run(ctx context.Context) error {
	path, err := p.resolvePath()
	if err != nil {
		return err
	}
	t := time.NewTicker(p.opts.Interval)
	defer t.Stop()
	// One immediate scan so the first MCP query after `tokenops up`
	// sees today's traffic instead of waiting for the first tick.
	p.scan(ctx, path)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.scan(ctx, path)
		}
	}
}

// Snapshot reads the cache once without diffing — useful for CLI
// status commands. Returns nil + error when the cache is unreadable.
func (p *Poller) Snapshot() (*StatsCache, error) {
	path, err := p.resolvePath()
	if err != nil {
		return nil, err
	}
	return Read(path)
}

// PublishCount reports how many envelopes the poller has emitted
// since boot. Used by the status command + the signal_quality
// upgrade classifier as a "is this signal alive?" check.
func (p *Poller) PublishCount() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishes
}

func (p *Poller) resolvePath() (string, error) {
	if p.opts.Path != "" {
		return p.opts.Path, nil
	}
	return DefaultPath()
}

// scan reads the cache and publishes one envelope per (date, model)
// where the cumulative token count grew since the last scan. Errors
// are logged + swallowed because the loop must stay alive across
// transient failures (Claude Code rewriting the file mid-read,
// disk-full warnings, etc).
func (p *Poller) scan(ctx context.Context, path string) {
	c, err := Read(path)
	if err != nil {
		// Missing file is the common case during early adoption (no
		// Claude Code installed) — log at debug so we don't spam logs.
		p.opts.Logger.Debug("claude-code stats cache unavailable", "path", path, "err", err)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, row := range c.DailyModelTokens {
		seen := p.lastSeen[row.Date]
		if seen == nil {
			seen = make(map[string]int64)
		}
		for model, total := range row.TokensByModel {
			prev := seen[model]
			if total <= prev {
				continue
			}
			delta := total - prev
			seen[model] = total
			if p.bus != nil {
				env, ok := newEnvelope(row.Date, model, delta, c.ModelUsage[model])
				if !ok {
					continue
				}
				p.bus.Publish(env)
				p.publishes++
			}
		}
		p.lastSeen[row.Date] = seen
		// Yield to ctx between row publishes so shutdown is snappy on
		// caches with many days.
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// newEnvelope wraps one (date, model, delta) tuple into a PromptEvent
// envelope. The cumulative Model summary is used to split delta into
// input/output buckets so spend.Engine can attach a price; without
// the split, downstream cost calc would charge everything as input.
// Returns ok=false when model is empty or delta is zero.
func newEnvelope(date, model string, delta int64, summary Model) (*eventschema.Envelope, bool) {
	if model == "" || delta <= 0 {
		return nil, false
	}
	input, output := splitDelta(delta, summary)
	// Deterministic ID: any restart of the poller would re-publish the
	// same envelope ID for the same (date, model, delta) tuple, which
	// the store dedups on. Built from a SHA-256 prefix of the inputs
	// so collisions across days are vanishingly unlikely.
	idHash := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d", date, model, delta))
	id := "ccc-" + hex.EncodeToString(idHash[:8])
	// Timestamp: end-of-day UTC for date. Daily granularity means we
	// can't do better than "this happened sometime today"; using EOD
	// keeps the event inside the day's bucket in hourly aggregations.
	ts, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, false
	}
	ts = ts.UTC().Add(23*time.Hour + 59*time.Minute)
	return &eventschema.Envelope{
		ID:            id,
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes: map[string]string{
			"granularity": "daily",
			"date":        date,
			"caveat":      "reads ~/.claude/stats-cache.json (undocumented Claude Code internal cache; daily totals split heuristically from lifetime ratios)",
		},
		Payload: &eventschema.PromptEvent{
			Provider:     eventschema.ProviderAnthropic,
			RequestModel: model,
			InputTokens:  input,
			OutputTokens: output,
			TotalTokens:  delta,
			Status:       200,
		},
	}, true
}

// splitDelta apportions delta into (input, output) using the
// model's cumulative input:output ratio. When the cumulative summary
// is empty (very fresh install or model never seen) we default to a
// 1:99 split — Claude Code workloads are almost entirely output +
// cache reads, so this errs on the side of undercounting input
// cost rather than overcharging the prompt side.
func splitDelta(delta int64, m Model) (input, output int64) {
	total := m.InputTokens + m.OutputTokens
	if total <= 0 {
		// Fallback split when we have no signal.
		input = delta / 100
		output = delta - input
		return
	}
	input = delta * m.InputTokens / total
	output = delta - input
	return
}
