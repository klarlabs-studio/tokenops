package plans

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventScanner is an EventReader that can also hand its events over one at
// a time, without building a slice of them.
//
// The daemon's event cache keeps a month of prompt events packed, and
// builds envelopes only when they are read. The plan headroom is computed
// every minute for the boundary figures, and each of its questions only
// looks at the events (which provider has subscription readings, the
// newest reading of a window, the tokens in a window): read as envelopes,
// that built a month of them, about 230 MB, every minute. Scanned, each
// event is built into one reused envelope.
//
// The envelope fn is handed, and its payload, are valid only until fn
// returns: fn must not keep them. Their strings and attribute map may be
// kept and must not be modified. Events come oldest first (in the order
// ReadEvents gives them), or newest first when newestFirst is set; fn
// returns false to stop.
type EventScanner interface {
	ScanEvents(ctx context.Context, t eventschema.EventType, since time.Time, newestFirst bool, fn func(*eventschema.Envelope) bool) error
}

// scanEvents scans r's prompt events at or after since, oldest first,
// skipping nil ones, through ScanEvents when r has it.
func scanEvents(ctx context.Context, r EventReader, since time.Time, fn func(*eventschema.Envelope)) error {
	if s, ok := r.(EventScanner); ok {
		return s.ScanEvents(ctx, eventschema.EventTypePrompt, since, false, func(e *eventschema.Envelope) bool {
			fn(e)
			return true
		})
	}
	events, err := r.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil {
		return err
	}
	for _, e := range events {
		if e != nil {
			fn(e)
		}
	}
	return nil
}

// heldEnvelope is a copy of an envelope a scan handed over, kept past the
// scan: the envelope and its prompt payload are copied, its strings and
// attributes shared.
type heldEnvelope struct {
	env    eventschema.Envelope
	prompt eventschema.PromptEvent
	set    bool
}

// keep copies e into h.
func (h *heldEnvelope) keep(e *eventschema.Envelope) {
	h.env, h.set = *e, true
	if p, ok := e.Payload.(*eventschema.PromptEvent); ok && p != nil {
		h.prompt = *p
		h.env.Payload = &h.prompt
	}
}

// get is the kept envelope, or nil.
func (h *heldEnvelope) get() *eventschema.Envelope {
	if !h.set {
		return nil
	}
	return &h.env
}

// newestWhere is the newest prompt event at or after since that match
// accepts, copied, or nil. Of several equally new, it is the first in the
// order ReadEvents gives, as a walk keeping the first strictly newer one
// finds.
func newestWhere(ctx context.Context, r EventReader, since time.Time, match func(*eventschema.Envelope) bool) (*eventschema.Envelope, error) {
	var best heldEnvelope
	s, ok := r.(EventScanner)
	if !ok {
		events, err := r.ReadEvents(ctx, eventschema.EventTypePrompt, since)
		if err != nil {
			return nil, err
		}
		var newest *eventschema.Envelope
		for _, e := range events {
			if e != nil && match(e) && (newest == nil || e.Timestamp.After(newest.Timestamp)) {
				newest = e
			}
		}
		if newest == nil {
			return nil, nil
		}
		best.keep(newest)
		return best.get(), nil
	}
	// Newest first, in time order: the first match is the newest, and the
	// matches with its time before it (in time order) are walked on to.
	err := s.ScanEvents(ctx, eventschema.EventTypePrompt, since, true, func(e *eventschema.Envelope) bool {
		if best.set && e.Timestamp.Before(best.env.Timestamp) {
			return false
		}
		if match(e) {
			best.keep(e)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return best.get(), nil
}
