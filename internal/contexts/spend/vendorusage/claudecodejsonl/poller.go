package claudecodejsonl

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

// SourceTag identifies envelopes emitted by this poller. Consumers
// (signal_quality classifier, dashboards) read this to upgrade
// Anthropic confidence to HIGH — this is real per-turn data from
// Claude Code's own conversation record, second only to Anthropic's
// own /usage endpoint.
const SourceTag = "claude-code-jsonl"

// PollerOptions configures the JSONL scanner.
type PollerOptions struct {
	// Root is the directory containing per-project session JSONLs.
	// Empty defaults to ~/.claude/projects.
	Root string
	// Interval is the gap between scans. Defaults to 30s — JSONLs
	// update on every turn so anything sub-minute catches activity
	// quickly without thrashing the filesystem.
	Interval time.Duration
	// Logger required for non-fatal errors (parse failures, missing
	// files).
	Logger *slog.Logger
}

// Poller diffs successive scans of the JSONL tree and publishes one
// PromptEvent per newly-seen assistant turn into the events bus.
// Dedup is by Anthropic message ID kept in-memory; across daemon
// restarts the store's envelope-ID dedup catches any replay because
// envelope IDs are deterministic per (message_id).
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu        sync.Mutex
	seen      map[string]struct{} // message IDs we've already emitted
	publishes int64
}

// NewPoller binds the bus + options. Bus may be nil — Snapshot() then
// returns parsed turns without emitting, useful for status commands.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{
		bus:  bus,
		opts: opts,
		seen: make(map[string]struct{}),
	}
}

// Run blocks until ctx is cancelled, scanning the JSONL tree on each
// tick. One immediate scan up-front so the first MCP query after
// `tokenops up` sees current activity.
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

// PublishCount reports envelopes emitted since boot.
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
	files, err := FindSessionFiles(root)
	if err != nil {
		p.opts.Logger.Debug("claude-code jsonl glob failed", "root", root, "err", err)
		return
	}
	for _, path := range files {
		if err := ReadFile(path, func(turn Turn) error {
			p.mu.Lock()
			if _, dup := p.seen[turn.MessageID]; dup {
				p.mu.Unlock()
				return nil
			}
			p.seen[turn.MessageID] = struct{}{}
			p.mu.Unlock()
			if p.bus != nil {
				env := newEnvelope(turn)
				p.bus.Publish(env)
				p.mu.Lock()
				p.publishes++
				p.mu.Unlock()
			}
			return nil
		}); err != nil {
			p.opts.Logger.Warn("claude-code jsonl read failed", "path", path, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// newEnvelope wraps one Turn as a PromptEvent envelope. Sum input +
// cache-read into InputTokens so spend.Engine attaches a cost based
// on Anthropic's blended cache pricing (rough — cache-read is priced
// lower than uncached input; future refinement can split the buckets
// via the Attributes map).
func newEnvelope(t Turn) *eventschema.Envelope {
	inputTokens := t.InputTokens + t.CacheReadInputTokens + t.CacheCreationInputTokens
	totalTokens := inputTokens + t.OutputTokens
	// Deterministic envelope ID per message — re-scanning the same
	// JSONL on poller restart never double-counts because the store
	// dedups on this ID.
	h := sha256.Sum256([]byte("claudecode-jsonl|" + t.MessageID))
	return &eventschema.Envelope{
		ID:            "ccj-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     t.Timestamp,
		Source:        SourceTag,
		Attributes: map[string]string{
			"session_id":           t.SessionID,
			"message_id":           t.MessageID,
			"service_tier":         t.ServiceTier,
			"input_uncached":       fmt.Sprintf("%d", t.InputTokens),
			"cache_read_input":     fmt.Sprintf("%d", t.CacheReadInputTokens),
			"cache_creation_input": fmt.Sprintf("%d", t.CacheCreationInputTokens),
		},
		Payload: &eventschema.PromptEvent{
			Provider:          eventschema.ProviderAnthropic,
			RequestModel:      t.Model,
			InputTokens:       inputTokens,
			CachedInputTokens: t.CacheReadInputTokens,
			OutputTokens:      t.OutputTokens,
			TotalTokens:       totalTokens,
			SessionID:         t.SessionID,
			// WorkflowID = "claude-code:<session>" so the workflow
			// reconstructor + waste detector treat each Claude Code
			// session as its own workflow. Surfaces context-growth
			// and oversized-context findings per session.
			WorkflowID: "claude-code:" + t.SessionID,
			Status:     200,
		},
	}
}
