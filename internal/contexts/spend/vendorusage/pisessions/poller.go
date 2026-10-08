package pisessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pollnow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller.
const SourceTag = "pi-sessions"

// PollerOptions configures the Pi transcript reader.
type PollerOptions struct {
	// Root is one session directory; empty reads DefaultRoots.
	Root string
	// Interval between scans; 30 seconds when zero.
	Interval time.Duration
	Logger   *slog.Logger
	// CostSource stamps each turn by its provider: plan-covered when a
	// plan of that provider is bound, metered otherwise. nil leaves it
	// unset.
	CostSource func(eventschema.Provider) eventschema.CostSource
}

// Poller emits one PromptEvent per assistant turn.
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu sync.Mutex
	// seen is every session|entry published, so a session in two roots
	// (Pi's and OMP's), or a file read again, counts once.
	seen map[string]struct{}
	// files is each transcript's size and mtime when last read.
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
	t := pollnow.NewTicker(ctx, p.opts.Interval)
	defer t.Stop()
	p.Scan(ctx, p.roots())
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.Scan(ctx, p.roots())
		}
	}
}

// roots is the configured root, or Pi's and OMP's that exist now: a
// directory created after start is picked up.
func (p *Poller) roots() []string {
	if p.opts.Root != "" {
		return []string{p.opts.Root}
	}
	roots, err := DefaultRoots()
	if err != nil {
		p.opts.Logger.Warn("pi sessions: no home directory", "err", err)
	}
	return roots
}

// Scan reads every transcript that changed since the last scan.
func (p *Poller) Scan(ctx context.Context, roots []string) {
	for _, root := range roots {
		files, err := FindSessionFiles(root)
		if err != nil {
			p.opts.Logger.Warn("pi sessions unreadable", "root", root, "err", err)
			continue
		}
		for _, path := range files {
			if ctx.Err() != nil {
				return
			}
			p.scanFile(ctx, path)
		}
	}
}

func (p *Poller) scanFile(ctx context.Context, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	stamp := fileStamp{size: info.Size(), mtime: info.ModTime()}
	if prev, ok := p.files[path]; ok && prev == stamp {
		return
	}
	if err := ReadFile(path, func(t Turn) error {
		key := t.SessionID + "|" + t.ID
		p.mu.Lock()
		_, dup := p.seen[key]
		p.seen[key] = struct{}{}
		p.mu.Unlock()
		if dup || p.bus == nil {
			return nil
		}
		var cost eventschema.CostSource
		if p.opts.CostSource != nil {
			cost = p.opts.CostSource(t.Provider)
		}
		if err := p.bus.PublishWait(ctx, newEnvelope(t, cost)); err != nil {
			return err
		}
		return ctx.Err()
	}); err != nil {
		p.opts.Logger.Warn("pi session unreadable", "path", path, "err", err)
		return
	}
	p.files[path] = stamp
}

// newEnvelope maps a turn to a PromptEvent; AgentID and WorkflowID follow
// the other readers' scheme so rollups group Pi sessions alike. Input is
// uncached input plus the cache's reads and writes, which ride along as
// their own counts, as Claude Code's transcripts are mapped.
func newEnvelope(t Turn, costSource eventschema.CostSource) *eventschema.Envelope {
	h := sha256.Sum256([]byte("pi|" + t.SessionID + "|" + t.ID))
	input := t.Input + t.CacheRead + t.CacheWrite
	return &eventschema.Envelope{
		ID:            "pi-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     t.Timestamp,
		Source:        SourceTag,
		Attributes: map[string]string{
			"granularity":          "assistant_turn",
			"session_id":           t.SessionID,
			"project":              t.Project,
			"message_id":           t.ID,
			"harness":              "pi",
			"pi_provider":          t.Backend,
			"input_uncached":       fmt.Sprintf("%d", t.Input),
			"cache_read_input":     fmt.Sprintf("%d", t.CacheRead),
			"cache_creation_input": fmt.Sprintf("%d", t.CacheWrite),
		},
		Payload: &eventschema.PromptEvent{
			Provider:                t.Provider,
			RequestModel:            t.Model,
			InputTokens:             input,
			CachedInputTokens:       t.CacheRead,
			CacheWriteInputTokens:   t.CacheWrite,
			CacheWrite1hInputTokens: t.CacheWrite1h,
			OutputTokens:            t.Output,
			TotalTokens:             input + t.Output,
			SessionID:               t.SessionID,
			AgentID:                 "pi:" + t.Project,
			WorkflowID:              "pi:" + t.Project + ":" + t.SessionID,
			Status:                  200,
			CostSource:              costSource,
		},
	}
}
