package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller.
const SourceTag = "opencode"

// PollerOptions configures the opencode SQLite scanner.
type PollerOptions struct {
	// Root is the opencode database path. Empty defaults to
	// ~/.local/share/opencode/opencode.db.
	Root string
	// Interval between scans. Defaults to 30s.
	Interval time.Duration
	// Logger required for non-fatal errors.
	Logger *slog.Logger
	// CostSource stamps every emitted PromptEvent. opencode is multi-provider,
	// so the daemon leaves this empty (metered) unless a matching flat-rate
	// plan is bound; see the daemon wiring.
	CostSource eventschema.CostSource
}

// Poller scans the opencode message table, dedupes turns by message ID, and
// emits one PromptEvent per assistant turn.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu        sync.Mutex
	seen      map[string]struct{}
	publishes int64
}

func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{bus: bus, opts: opts, seen: make(map[string]struct{})}
}

func (p *Poller) Run(ctx context.Context) error {
	root, err := p.resolveRoot()
	if err != nil {
		return err
	}
	t := time.NewTicker(p.opts.Interval)
	defer t.Stop()
	p.scan(ctx, root)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.scan(ctx, root)
		}
	}
}

func (p *Poller) PublishCount() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishes
}

func (p *Poller) resolveRoot() (string, error) {
	if p.opts.Root != "" {
		return p.opts.Root, nil
	}
	return DefaultRoot()
}

func (p *Poller) scan(ctx context.Context, root string) {
	if err := ReadMessages(root, func(turn Turn) error {
		p.mu.Lock()
		if _, dup := p.seen[turn.ID]; dup {
			p.mu.Unlock()
			return nil
		}
		p.seen[turn.ID] = struct{}{}
		p.mu.Unlock()
		if p.bus != nil {
			p.bus.Publish(newEnvelope(turn, p.opts.CostSource))
			p.mu.Lock()
			p.publishes++
			p.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}); err != nil {
		p.opts.Logger.Warn("opencode db read failed", "path", root, "err", err)
	}
}

// newEnvelope maps an opencode Turn to a PromptEvent envelope. AgentID and
// WorkflowID mirror the Claude Code / Codex attribution scheme so group=agent
// rollups and the waste detector resolve opencode sessions the same way.
func newEnvelope(t Turn, costSource eventschema.CostSource) *eventschema.Envelope {
	h := sha256.Sum256([]byte("opencode|" + t.ID))
	attrs := map[string]string{
		"granularity":  "assistant_turn",
		"session_id":   t.SessionID,
		"project":      t.Project,
		"message_id":   t.ID,
		"provider_id":  string(t.Provider),
		"cached_input": fmt.Sprintf("%d", t.CachedTokens),
		"cost_usd":     fmt.Sprintf("%.6f", t.Cost),
	}
	return &eventschema.Envelope{
		ID:            "ocj-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     t.Timestamp,
		Source:        SourceTag,
		Attributes:    attrs,
		Payload: &eventschema.PromptEvent{
			Provider:          t.Provider,
			RequestModel:      t.Model,
			InputTokens:       int64(t.InputTokens),
			CachedInputTokens: int64(t.CachedTokens),
			OutputTokens:      int64(t.OutputTokens),
			TotalTokens:       int64(t.InputTokens + t.OutputTokens),
			SessionID:         t.SessionID,
			AgentID:           "opencode:" + t.Project,
			WorkflowID:        "opencode:" + t.Project + ":" + t.SessionID,
			Status:            200,
			CostSource:        costSource,
		},
	}
}
