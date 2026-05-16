package cursor

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
// promotes to HIGH on observation with an explicit caveat that the
// underlying endpoint is undocumented.
const SourceTag = "cursor-web"

// PollerOptions configures the periodic call to cursor.com/api/usage.
type PollerOptions struct {
	Cookie   string
	UserID   string
	Interval time.Duration
	BaseURL  string // test override
	Logger   *slog.Logger
}

// Poller fetches one /api/usage snapshot per tick and emits one
// envelope per (model row) so per-model aggregation in the store
// stays clean.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu          sync.Mutex
	client      *Client
	publishes   int64
	lastErr     error
	lastErrTime time.Time
	lastSeenKey string // dedup key per response, prevents same-snapshot double-emit
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

// LastError exposes the most recent scan error.
func (p *Poller) LastError() (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErrTime, p.lastErr
}

func (p *Poller) ensureClient() error {
	if p.opts.Cookie == "" || p.opts.UserID == "" {
		return ErrMissingCredential
	}
	c := NewClient(p.opts.Cookie, p.opts.UserID)
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
	// Build a dedup key from the response contents + start-of-month
	// (Cursor doesn't expose a freshness timestamp). Same snapshot →
	// same key → skip emit.
	key := snapshotKey(resp)
	p.mu.Lock()
	if key != "" && key == p.lastSeenKey {
		p.mu.Unlock()
		return
	}
	p.lastSeenKey = key
	p.mu.Unlock()
	ts := time.Now().UTC()
	for model, m := range resp.Models {
		env := newEnvelope(ts, p.opts.UserID, resp.StartOfMonth, model, m)
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

// snapshotKey builds a stable string that changes only when the
// observed numbers do — used for poll-level dedup.
func snapshotKey(resp *UsageResponse) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "som=%s|", resp.StartOfMonth)
	// Map iteration order isn't deterministic; we don't need order
	// stability here because the hash collapses any permutation of
	// the same content to the same digest is not guaranteed... so
	// sort keys.
	keys := make([]string, 0, len(resp.Models))
	for k := range resp.Models {
		keys = append(keys, k)
	}
	// Sort in a tiny inline pass to avoid pulling in sort.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	for _, k := range keys {
		m := resp.Models[k]
		_, _ = fmt.Fprintf(h, "%s=%d/%d|", k, m.NumRequests, m.MaxRequestUsage)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// newEnvelope wraps one (model, snapshot) pair as a PromptEvent.
// NumRequests is rolled into TotalTokens as a coarse proxy — Cursor
// bills by request count not tokens; the future Cursor-aware
// session_budget should read the exact counts from Attributes.
func newEnvelope(ts time.Time, userID, startOfMonth, model string, m ModelUsage) *eventschema.Envelope {
	h := sha256.Sum256([]byte("cursor-web|" + userID + "|" + startOfMonth + "|" + model + "|" + fmt.Sprintf("%d", m.NumRequests)))
	pct := -1.0
	if m.MaxRequestUsage > 0 {
		pct = 100.0 * float64(m.NumRequests) / float64(m.MaxRequestUsage)
	}
	return &eventschema.Envelope{
		ID:            "cur-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        SourceTag,
		Attributes: map[string]string{
			"user_id":           userID,
			"start_of_month":    startOfMonth,
			"num_requests":      fmt.Sprintf("%d", m.NumRequests),
			"max_request_usage": fmt.Sprintf("%d", m.MaxRequestUsage),
			"used_pct":          fmt.Sprintf("%.2f", pct),
		},
		Payload: &eventschema.PromptEvent{
			Provider:     eventschema.ProviderCursor,
			RequestModel: model,
			Status:       200,
		},
	}
}
