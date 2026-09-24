package domainevents

import (
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EnvelopePublisher accepts canonical envelopes. The canonical events.Bus
// satisfies this interface without coupling domainevents to its transport.
type EnvelopePublisher interface {
	Publish(*eventschema.Envelope)
}

// ToEnvelope converts a legacy typed domain event into the canonical event
// envelope. The event's kind and typed JSON body remain intact while gaining
// the shared identity, schema version, source and association fields.
func ToEnvelope(ev Event) (*eventschema.Envelope, error) {
	if ev == nil {
		return nil, errors.New("domainevents: cannot envelope a nil event")
	}
	var data []byte
	if replayed, ok := ev.(interface{ Payload() json.RawMessage }); ok {
		data = replayed.Payload()
		if len(data) == 0 {
			data = []byte("null")
		}
	} else {
		var err error
		data, err = json.Marshal(ev)
		if err != nil {
			return nil, err
		}
	}
	at := time.Now().UTC()
	if timed, ok := ev.(interface{ OccurredAt() time.Time }); ok && !timed.OccurredAt().IsZero() {
		at = timed.OccurredAt().UTC()
	} else if replayed, ok := ev.(interface{ At() time.Time }); ok && !replayed.At().IsZero() {
		at = replayed.At().UTC()
	}
	return &eventschema.Envelope{
		ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeDomain, Timestamp: at, Source: "domain_bus",
		Payload: &eventschema.DomainEvent{Kind: ev.Kind(), Data: data},
	}, nil
}

// BridgeToEnvelopeBus temporarily mirrors domain events into the canonical
// envelope stream. It returns the subscription so the composition root can
// detach it during shutdown or after migration is complete.
func BridgeToEnvelopeBus(source *Bus, target EnvelopePublisher, logger *slog.Logger) *Subscription {
	if source == nil || target == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return source.Subscribe("*", func(ev Event) {
		env, err := ToEnvelope(ev)
		if err != nil {
			logger.Error("domain event envelope conversion failed", "kind", ev.Kind(), "err", err)
			return
		}
		target.Publish(env)
	})
}
