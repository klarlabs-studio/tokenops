package copilotcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pollnow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller.
const SourceTag = "copilot-cli"

// Granularity marks an event as one session's usage of one model between
// two of its shutdowns: priced from its tokens, since nothing else carries
// them, but not one message on a plan's meter.
const Granularity = "session"

// PollerOptions configures the Copilot CLI reader.
type PollerOptions struct {
	// Root is Copilot CLI's session-state directory; empty takes
	// DefaultRoot.
	Root string
	// Interval between scans; 30 seconds when zero.
	Interval time.Duration
	Logger   *slog.Logger
	// CostSource stamps each event: plan-covered when a Copilot plan is
	// bound, metered otherwise.
	CostSource eventschema.CostSource
}

// Poller emits one PromptEvent per model for what each session shutdown
// adds.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu   sync.Mutex
	seen map[string]struct{}
	// files is each log's size and mtime when last read, so an unchanged
	// one is not parsed again.
	files map[string]fileStamp
}

type fileStamp struct {
	size  int64
	mtime time.Time
}

// NewPoller builds the reader.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
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
	t := pollnow.NewTicker(ctx, p.opts.Interval)
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

// Scan reads every session log that changed since the last scan.
func (p *Poller) Scan(ctx context.Context, root string) {
	files, err := FindSessionFiles(root)
	if err != nil {
		p.opts.Logger.Warn("copilot cli sessions unreadable", "root", root, "err", err)
		return
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		stamp := fileStamp{size: info.Size(), mtime: info.ModTime()}
		if prev, ok := p.files[path]; ok && prev == stamp {
			continue
		}
		if err := ReadFile(path, func(u Usage) error {
			key := u.SessionID + "|" + u.ID
			p.mu.Lock()
			_, dup := p.seen[key]
			p.seen[key] = struct{}{}
			p.mu.Unlock()
			if dup || p.bus == nil {
				return nil
			}
			if err := p.bus.PublishWait(ctx, newEnvelope(u, p.opts.CostSource)); err != nil {
				return err
			}
			return ctx.Err()
		}); err != nil {
			p.opts.Logger.Warn("copilot cli session unreadable", "path", path, "err", err)
			continue
		}
		p.files[path] = stamp
	}
}

// newEnvelope maps a usage to a PromptEvent; AgentID and WorkflowID follow
// the other readers' scheme so rollups group Copilot CLI sessions alike.
// Input is the total, cache included, which rides along as its own counts.
func newEnvelope(u Usage, costSource eventschema.CostSource) *eventschema.Envelope {
	h := sha256.Sum256([]byte("copilot-cli|" + u.SessionID + "|" + u.ID))
	input := u.InputTotal()
	return &eventschema.Envelope{
		ID:            "cpl-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     u.Timestamp,
		Source:        SourceTag,
		Attributes: map[string]string{
			"granularity":      Granularity,
			"session_id":       u.SessionID,
			"project":          u.Project,
			"requests":         strconv.FormatInt(u.Requests, 10),
			"reasoning_output": strconv.FormatInt(u.Reasoning, 10),
		},
		Payload: &eventschema.PromptEvent{
			Provider:              eventschema.ProviderGitHub,
			RequestModel:          u.Model,
			InputTokens:           input,
			CachedInputTokens:     u.CacheRead,
			CacheWriteInputTokens: u.CacheWrite,
			OutputTokens:          u.Output,
			TotalTokens:           input + u.Output,
			SessionID:             u.SessionID,
			AgentID:               "copilot-cli:" + u.Project,
			WorkflowID:            "copilot-cli:" + u.Project + ":" + u.SessionID,
			Status:                200,
			CostSource:            costSource,
		},
	}
}
