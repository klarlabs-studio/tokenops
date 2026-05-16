package copilot

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
// classifier upgrades GitHub Copilot confidence to HIGH on observation.
const SourceTag = "github-copilot"

// PollerOptions configures the periodic call to /copilot_internal/user.
type PollerOptions struct {
	// OAuthToken can be set explicitly (env / config); empty means
	// "load from disk via TokenPaths".
	OAuthToken string
	// TokenPaths overrides the default discovery locations. Empty
	// uses DefaultTokenPaths.
	TokenPaths []string
	// Interval defaults to 2 minutes. GitHub doesn't document a rate
	// limit on this endpoint but the IDE plugins poll on similar
	// cadence.
	Interval time.Duration
	// Logger required.
	Logger *slog.Logger
	// BaseURL override for tests.
	BaseURL string
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
	client      *Client
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

// LastError exposes the most recent scan error + when it happened.
// Returned with time first so revive's error-must-be-last lint stays
// happy.
func (p *Poller) LastError() (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErrTime, p.lastErr
}

func (p *Poller) ensureClient() error {
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
		t, err := LoadToken(paths)
		if err != nil {
			return err
		}
		token = t
	}
	c := NewClient(token)
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
	for key, snap := range resp.QuotaSnapshots {
		env := newEnvelope(ts, resp, key, snap)
		if p.bus != nil {
			p.bus.Publish(env)
			p.mu.Lock()
			p.publishes++
			p.mu.Unlock()
		}
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

// newEnvelope wraps one quota snapshot as a PromptEvent envelope.
// Copilot doesn't emit per-turn token counts; the response is a
// rolling quota state. We encode it as a zero-token envelope with
// the live percent_remaining + entitlement in Attributes so the
// signal_quality classifier and future Copilot-aware session_budget
// can read it.
func newEnvelope(ts time.Time, resp *UserResponse, snapshotKey string, snap QuotaSnapshot) *eventschema.Envelope {
	h := sha256.Sum256([]byte("github-copilot|" + resp.Login + "|" + resp.TimestampUTC + "|" + snapshotKey))
	return &eventschema.Envelope{
		ID:            "ghc-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes: map[string]string{
			"login":             resp.Login,
			"snapshot":          snapshotKey,
			"percent_remaining": fmt.Sprintf("%.2f", snap.PercentRemaining),
			"remaining":         fmt.Sprintf("%.2f", snap.Remaining),
			"entitlement":       fmt.Sprintf("%d", snap.Entitlement),
			"overage_count":     fmt.Sprintf("%d", snap.OverageCount),
			"unlimited":         fmt.Sprintf("%t", snap.Unlimited),
			"quota_reset_date":  resp.QuotaResetDate,
			"chat_enabled":      fmt.Sprintf("%t", resp.ChatEnabled),
		},
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderGitHub,
			Status:   200,
		},
	}
}
