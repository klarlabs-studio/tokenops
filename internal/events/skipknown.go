package events

import (
	"context"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SkipKnown wraps bus so envelopes whose ID known reports as already stored
// never reach it. Pollers keep their dedup state in memory and re-read their
// whole source on every daemon start; the store would discard those
// envelopes on conflict, but only after spending a write transaction on each
// batch while every other process sharing the store waits. A nil known
// returns bus unchanged.
func SkipKnown(bus Bus, known func(id string) bool) Bus {
	if known == nil {
		return bus
	}
	return &skipKnownBus{Bus: bus, known: known}
}

type skipKnownBus struct {
	Bus
	known func(id string) bool
}

// Publish forwards env unless it is already stored.
func (b *skipKnownBus) Publish(env *eventschema.Envelope) {
	if env != nil && b.known(env.ID) {
		return
	}
	b.Bus.Publish(env)
}

// PublishWait forwards env unless it is already stored. A stored envelope
// reports success: it is persisted, which is what the caller waits for.
func (b *skipKnownBus) PublishWait(ctx context.Context, env *eventschema.Envelope) error {
	if env != nil && b.known(env.ID) {
		return nil
	}
	return b.Bus.PublishWait(ctx, env)
}
