// Package cursorturns ingests Cursor's per-turn usage into the event
// store.
//
// Cursor keeps no per-turn record on disk — its usage endpoint reports
// plan consumption, a percentage rather than tokens, which is why the
// capability matrix listed Cursor spend as quota only. Its stop hook
// does report tokens, and internal/infra/cursorturns records each one as
// it arrives. This reads that ledger.
//
// The result is that Cursor joins every other client in spend, burn
// rate, forecast and top consumers, instead of being a percentage in a
// headroom view.
package cursorturns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/cursorturns"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag marks events from this reader so rollups can include or
// exclude them by name.
const SourceTag = "cursor-hook"

// PollerOptions configures the ledger poller.
type PollerOptions struct {
	// Dir is the ledger directory; empty takes the default.
	Dir string
	// Interval between reads. Zero takes one minute.
	Interval time.Duration
	// PlanCovered reports whether Cursor usage is included in a plan the
	// operator pays a flat rate for, which decides whether these events
	// are metered spend or plan-included.
	PlanCovered bool
	Logger      *slog.Logger
}

// Poller reads the ledger into the bus.
type Poller struct {
	bus  events.Bus
	opts PollerOptions
}

// NewPoller builds the ledger poller.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = time.Minute
	}
	return &Poller{bus: bus, opts: opts}
}

// Run polls until ctx ends.
//
// The whole ledger is re-read each tick and every turn re-emitted. That
// is deliberate: the envelope id is derived from the generation id and
// the store drops a repeat on conflict, so re-reading is idempotent and
// needs no marker that could drift out of step with what was stored.
func (p *Poller) Run(ctx context.Context) error {
	tick := time.NewTicker(p.opts.Interval)
	defer tick.Stop()
	p.once()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			p.once()
		}
	}
}

func (p *Poller) once() {
	turns, err := cursorturns.Read(p.opts.Dir)
	if err != nil {
		if p.opts.Logger != nil {
			p.opts.Logger.Warn("cursor turn ledger unreadable", "err", err)
		}
		return
	}
	for _, t := range turns {
		if env := NewEnvelope(t, p.costSource()); env != nil {
			p.bus.Publish(env)
		}
	}
}

func (p *Poller) costSource() eventschema.CostSource {
	if p.opts.PlanCovered {
		return eventschema.CostSourcePlanIncluded
	}
	return eventschema.CostSourceMetered
}

// NewEnvelope maps one recorded turn to a PromptEvent.
//
// Cursor's cache figures sit INSIDE input_tokens — its own team states
// it: "input_tokens is inclusive of cache_read_tokens and
// cache_write_tokens". The event schema keeps the two separate, so the
// cached figure is reported on its own and InputTokens carries the whole
// input side exactly once. Summing them would bill the cached tokens
// twice.
func NewEnvelope(t cursorturns.Turn, costSource eventschema.CostSource) *eventschema.Envelope {
	if !t.Reported() || t.GenerationID == "" {
		return nil
	}
	model := t.ModelName()
	if model == "" {
		return nil
	}
	input, output := deref(t.InputTokens), deref(t.OutputTokens)
	read, write := deref(t.CacheReadTokens), deref(t.CacheWriteTokens)

	// One event per generation. Cursor reports the same cumulative
	// figures on stop and afterAgentResponse for one generation, so an id
	// keyed on it is what stops a turn being counted twice.
	h := sha256.Sum256([]byte("cursor-hook|" + t.GenerationID))
	return &eventschema.Envelope{
		ID:            "cur-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     t.TS,
		Source:        SourceTag,
		Attributes: map[string]string{
			"granularity":   "assistant_turn",
			"session_id":    t.ConversationID,
			"generation_id": t.GenerationID,
			"cached_input":  fmt.Sprintf("%d", read),
			"cache_write":   fmt.Sprintf("%d", write),
			"composer_slug": t.Model,
		},
		Payload: &eventschema.PromptEvent{
			Provider:          eventschema.ProviderCursor,
			RequestModel:      model,
			InputTokens:       input,
			CachedInputTokens: read,
			OutputTokens:      output,
			TotalTokens:       input + output,
			SessionID:         t.ConversationID,
			// Mirrors the attribution every other client uses, so
			// group=agent rollups and the waste detector resolve Cursor
			// sessions the same way.
			AgentID:    "cursor",
			WorkflowID: "cursor:" + t.ConversationID,
			Status:     200,
			CostSource: costSource,
		},
	}
}

func deref(p *int64) int64 {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
}
