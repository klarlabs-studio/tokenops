package anthropic

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

// SourceTag identifies envelopes published by the Anthropic Admin API
// poller. Consumers (signal_quality classifier, dashboards) read this
// to upgrade Anthropic confidence to "high" — the strongest signal
// TokenOps offers, because the data comes directly from Anthropic's
// own usage_report endpoint.
const SourceTag = "vendor-usage-anthropic"

// PollerOptions configures the periodic poller.
type PollerOptions struct {
	// AdminKey is the sk-ant-admin-* key. Required.
	AdminKey string
	// Interval is the gap between polls. Defaults to 5 minutes — the
	// API freshness lag is ~5 minutes so anything tighter wastes
	// quota without surfacing new data.
	Interval time.Duration
	// BucketWidth determines the response granularity. Defaults to 1h
	// (168-bucket cap = 7 days of history). Set to 1d for sparser
	// long-term polling.
	BucketWidth BucketWidth
	// LookbackOnFirstScan is how far back the first poll fetches. After
	// that the poller tracks its own cursor and only pulls deltas.
	// Defaults to 6h so a freshly-started daemon shows recent history.
	LookbackOnFirstScan time.Duration
	// Logger is required for non-fatal failures (network blips, 401s).
	Logger *slog.Logger
}

// Poller wraps AdminClient with a periodic loop that publishes one
// envelope per (bucket, model) cell into the events bus. State (the
// "last bucket end timestamp" cursor) is kept in-memory; a restart
// re-queries from now-LookbackOnFirstScan so we lose at most a few
// hours of catch-up. Duplicate envelopes carry deterministic IDs and
// the store dedups them, so the overlap is harmless.
type Poller struct {
	client *AdminClient
	bus    events.Bus
	opts   PollerOptions

	mu          sync.Mutex
	lastCursor  time.Time
	publishes   int64
	lastErr     error
	lastErrTime time.Time
}

// NewPoller binds client + bus + options. Bus may be nil for status
// commands that want only Snapshot.
func NewPoller(client *AdminClient, bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Minute
	}
	if opts.BucketWidth == "" {
		opts.BucketWidth = BucketWidthHour
	}
	if opts.LookbackOnFirstScan <= 0 {
		opts.LookbackOnFirstScan = 6 * time.Hour
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{
		client: client,
		bus:    bus,
		opts:   opts,
	}
}

// Run blocks until ctx is cancelled. Errors are logged + stored on
// the poller (visible via LastError) but never propagated, so a
// transient 5xx doesn't kill the loop.
func (p *Poller) Run(ctx context.Context) error {
	if p.opts.AdminKey == "" {
		return ErrMissingAdminKey
	}
	// First scan reaches back LookbackOnFirstScan; subsequent scans
	// pick up where the cursor left off.
	p.scan(ctx)
	t := time.NewTicker(p.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.scan(ctx)
		}
	}
}

// PublishCount reports the number of envelopes emitted since boot.
// Used by status commands + the signal_quality classifier.
func (p *Poller) PublishCount() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishes
}

// LastError exposes the most recent scan error + when it happened, so
// the CLI status command can show why the signal isn't refreshing.
// The time is the first return value to satisfy revive's
// error-must-be-last convention.
func (p *Poller) LastError() (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErrTime, p.lastErr
}

func (p *Poller) scan(ctx context.Context) {
	p.mu.Lock()
	start := p.lastCursor
	p.mu.Unlock()
	if start.IsZero() {
		start = time.Now().Add(-p.opts.LookbackOnFirstScan)
	}
	req := MessagesUsageRequest{
		StartingAt:  start,
		EndingAt:    time.Now(),
		BucketWidth: p.opts.BucketWidth,
		GroupBy:     []string{"model"},
	}
	resp, err := p.client.MessagesUsage(ctx, req)
	if err != nil {
		p.recordErr(err)
		// Don't log key-missing case at warn level — that's a
		// configuration state, not a runtime fault.
		if errors.Is(err, ErrMissingAdminKey) {
			p.opts.Logger.Debug("anthropic admin key missing; poller idle")
			return
		}
		p.opts.Logger.Warn("anthropic admin usage poll failed", "err", err)
		return
	}
	p.recordSuccess()
	var newCursor time.Time
	for _, bucket := range resp.Data {
		for _, r := range bucket.Results {
			env, ok := newEnvelope(bucket.StartingAt, bucket.EndingAt, r)
			if !ok {
				continue
			}
			if p.bus != nil {
				p.bus.Publish(env)
				p.mu.Lock()
				p.publishes++
				p.mu.Unlock()
			}
		}
		if bucket.EndingAt.After(newCursor) {
			newCursor = bucket.EndingAt
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
	if !newCursor.IsZero() {
		p.mu.Lock()
		p.lastCursor = newCursor
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

// newEnvelope wraps one UsageResult into a PromptEvent envelope.
// Timestamp is the bucket's StartingAt so the event lands in the
// correct hourly aggregation bucket. The Anthropic Admin response
// splits input into uncached + cache-read + cache-creation; we sum
// uncached + cache-read into InputTokens (both are "input the model
// processed") and surface the cache-creation split via Attributes
// for future, more precise cost recompute.
// NewEnvelope is the exported form of newEnvelope so backfill paths
// (CLI command) can mint envelopes from arbitrary UsageResult tuples
// without going through the running poller. The deterministic ID
// keeps backfill output dedupable against poller output.
func NewEnvelope(startsAt, endsAt time.Time, r UsageResult) (*eventschema.Envelope, bool) {
	return newEnvelope(startsAt, endsAt, r)
}

func newEnvelope(startsAt, endsAt time.Time, r UsageResult) (*eventschema.Envelope, bool) {
	if r.Model == "" {
		return nil, false
	}
	inputTokens := r.UncachedInputTokens + r.CacheReadInputTokens
	totalTokens := inputTokens + r.OutputTokens
	if totalTokens == 0 {
		return nil, false
	}
	attrs := map[string]string{
		"granularity":         "bucket",
		"bucket_start":        startsAt.UTC().Format(time.RFC3339),
		"bucket_end":          endsAt.UTC().Format(time.RFC3339),
		"cache_read_input":    fmt.Sprintf("%d", r.CacheReadInputTokens),
		"cache_creation_5m":   fmt.Sprintf("%d", r.CacheCreation.Ephemeral5mInputTokens),
		"cache_creation_1h":   fmt.Sprintf("%d", r.CacheCreation.Ephemeral1hInputTokens),
		"web_search_requests": fmt.Sprintf("%d", r.ServerToolUse.WebSearchRequests),
	}
	if r.WorkspaceID != nil {
		attrs["workspace_id"] = *r.WorkspaceID
	}
	if r.APIKeyID != nil {
		attrs["api_key_id"] = *r.APIKeyID
	}
	if r.ServiceTier != "" {
		attrs["service_tier"] = r.ServiceTier
	}
	if r.ContextWindow != "" {
		attrs["context_window"] = r.ContextWindow
	}
	if r.InferenceGeo != "" {
		attrs["inference_geo"] = r.InferenceGeo
	}
	// Deterministic ID: SHA-256 of (bucket_start, model, key + workspace).
	// Re-publishing the same bucket on poller restart produces the
	// same ID; the store dedups so overlap is harmless.
	keyMaterial := fmt.Sprintf("%s|%s|%s|%s",
		startsAt.UTC().Format(time.RFC3339), r.Model,
		safeStr(r.APIKeyID), safeStr(r.WorkspaceID))
	h := sha256.Sum256([]byte(keyMaterial))
	return &eventschema.Envelope{
		ID:            "ana-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     startsAt.UTC(),
		Source:        SourceTag,
		Attributes:    attrs,
		Payload: &eventschema.PromptEvent{
			Provider:     eventschema.ProviderAnthropic,
			RequestModel: r.Model,
			InputTokens:  inputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens:  totalTokens,
			Status:       200,
		},
	}, true
}

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
