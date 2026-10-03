package geminicli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller.
const SourceTag = "gemini-cli"

// PollerOptions configures the Gemini CLI reader.
type PollerOptions struct {
	// Root is Gemini CLI's tmp directory; empty takes DefaultRoot.
	Root string
	// Interval between scans; 30 seconds when zero.
	Interval time.Duration
	Logger   *slog.Logger
	// CostSource stamps each turn: plan-covered when a Gemini plan is
	// bound, metered otherwise.
	CostSource eventschema.CostSource
	Now        func() time.Time
}

// Poller emits one PromptEvent per settled model turn.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu   sync.Mutex
	seen map[string]struct{}
	// files is each recording's size and mtime when last read, so an
	// unchanged one is not parsed again.
	files map[string]fileStamp
}

type fileStamp struct {
	size  int64
	mtime time.Time
	// settled is whether the read saw the file idle, so its last message
	// was taken; an unsettled read is retried once the file goes quiet.
	settled bool
}

// NewPoller builds the reader.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Poller{bus: bus, opts: opts, seen: map[string]struct{}{}, files: map[string]fileStamp{}}
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) error {
	root := p.opts.Root
	if root == "" {
		r, err := DefaultRoot()
		if err != nil {
			return err
		}
		root = r
	}
	t := time.NewTicker(p.opts.Interval)
	defer t.Stop()
	p.Scan(ctx, root)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.Scan(ctx, root)
		}
	}
}

// Scan reads every recording that changed since the last scan.
func (p *Poller) Scan(ctx context.Context, root string) {
	files, err := FindSessionFiles(root)
	if err != nil {
		p.opts.Logger.Warn("gemini cli sessions unreadable", "root", root, "err", err)
		return
	}
	now := p.opts.Now()
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		stamp := fileStamp{size: info.Size(), mtime: info.ModTime(), settled: now.Sub(info.ModTime()) >= settled}
		if prev, ok := p.files[path]; ok && prev == stamp {
			continue
		}
		if err := ReadFile(path, now, func(t Turn) error {
			p.mu.Lock()
			_, dup := p.seen[t.ID]
			p.seen[t.ID] = struct{}{}
			p.mu.Unlock()
			if dup || p.bus == nil {
				return nil
			}
			if err := p.bus.PublishWait(ctx, newEnvelope(t, p.opts.CostSource)); err != nil {
				return err
			}
			return ctx.Err()
		}); err != nil {
			p.opts.Logger.Warn("gemini cli session unreadable", "path", path, "err", err)
			continue
		}
		p.files[path] = stamp
	}
}

// newEnvelope maps a turn to a PromptEvent; AgentID and WorkflowID follow
// the other readers' scheme so rollups group Gemini CLI sessions alike.
func newEnvelope(t Turn, costSource eventschema.CostSource) *eventschema.Envelope {
	h := sha256.Sum256([]byte("gemini-cli|" + t.SessionID + "|" + t.ID))
	return &eventschema.Envelope{
		ID:            "gem-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     t.Timestamp,
		Source:        SourceTag,
		Attributes: map[string]string{
			"granularity":  "assistant_turn",
			"session_id":   t.SessionID,
			"project":      t.Project,
			"message_id":   t.ID,
			"cached_input": fmt.Sprintf("%d", t.CachedTokens),
		},
		Payload: &eventschema.PromptEvent{
			Provider:          eventschema.ProviderGemini,
			RequestModel:      t.Model,
			InputTokens:       int64(t.InputTokens),
			CachedInputTokens: int64(t.CachedTokens),
			OutputTokens:      int64(t.OutputTokens),
			TotalTokens:       int64(t.InputTokens + t.OutputTokens),
			SessionID:         t.SessionID,
			AgentID:           "gemini-cli:" + t.Project,
			WorkflowID:        "gemini-cli:" + t.Project + ":" + t.SessionID,
			Status:            200,
			CostSource:        costSource,
		},
	}
}
