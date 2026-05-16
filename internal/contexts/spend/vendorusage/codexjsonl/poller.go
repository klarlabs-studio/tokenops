package codexjsonl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller. signal_quality
// promotes to HIGH on observation — Codex's token_count records carry
// OpenAI's authoritative rate-limit percentages.
const SourceTag = "codex-jsonl"

// PollerOptions configures the Codex JSONL scanner.
type PollerOptions struct {
	// Root defaults to ~/.codex/sessions.
	Root string
	// Interval between scans. Defaults to 30s.
	Interval time.Duration
	// Logger required for non-fatal errors.
	Logger *slog.Logger
}

// Poller scans Codex session JSONLs, dedupes turns by (sessionID, sequence)
// since token_count records don't carry a server-side message ID, and
// emits one PromptEvent per turn.
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
	files, err := FindSessionFiles(root)
	if err != nil {
		p.opts.Logger.Debug("codex jsonl glob failed", "root", root, "err", err)
		return
	}
	for _, path := range files {
		if err := ReadFile(path, func(turn Turn) error {
			key := turn.SessionID + "|" + strconv.Itoa(turn.RecordSequence)
			p.mu.Lock()
			if _, dup := p.seen[key]; dup {
				p.mu.Unlock()
				return nil
			}
			p.seen[key] = struct{}{}
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
			p.opts.Logger.Warn("codex jsonl read failed", "path", path, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// newEnvelope maps a Codex Turn to a PromptEvent envelope. Provider
// is OpenAI. InputTokens rolls cached + uncached + reasoning so the
// total cost calc reflects Codex's billing surface. The rate_limits
// block is serialized into Attributes so the signal_quality classifier
// (and a future Codex-aware session_budget tool) can read it without
// re-parsing the source file.
func newEnvelope(t Turn) *eventschema.Envelope {
	inputTokens := t.InputTokens
	totalTokens := inputTokens + t.OutputTokens + t.ReasoningTok
	h := sha256.Sum256([]byte("codex-jsonl|" + t.SessionID + "|" + strconv.Itoa(t.RecordSequence)))
	attrs := map[string]string{
		"session_id":           t.SessionID,
		"sequence":             strconv.Itoa(t.RecordSequence),
		"cached_input":         fmt.Sprintf("%d", t.CachedTokens),
		"reasoning_output":     fmt.Sprintf("%d", t.ReasoningTok),
		"context_window":       fmt.Sprintf("%d", t.ContextWindow),
		"plan_type":            t.RateLimits.PlanType,
		"primary_used_pct":     fmt.Sprintf("%.2f", t.RateLimits.PrimaryUsedPercent),
		"primary_window_min":   fmt.Sprintf("%d", t.RateLimits.PrimaryWindowMinutes),
		"primary_resets_at":    fmt.Sprintf("%d", t.RateLimits.PrimaryResetsAtUnix),
		"secondary_used_pct":   fmt.Sprintf("%.2f", t.RateLimits.SecondaryUsedPercent),
		"secondary_window_min": fmt.Sprintf("%d", t.RateLimits.SecondaryWindowMinutes),
		"secondary_resets_at":  fmt.Sprintf("%d", t.RateLimits.SecondaryResetsAtUnix),
	}
	return &eventschema.Envelope{
		ID:            "cdx-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     t.Timestamp,
		Source:        SourceTag,
		Attributes:    attrs,
		Payload: &eventschema.PromptEvent{
			Provider:     eventschema.ProviderOpenAI,
			RequestModel: t.Model,
			InputTokens:  inputTokens,
			OutputTokens: t.OutputTokens + t.ReasoningTok,
			TotalTokens:  totalTokens,
			Status:       200,
		},
	}
}
