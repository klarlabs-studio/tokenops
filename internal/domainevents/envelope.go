package domainevents

import (
	"bytes"
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
		Association: associationFor(ev.Kind(), data),
		Payload:     &eventschema.DomainEvent{Kind: ev.Kind(), Data: data},
	}, nil
}

// EnvelopeFromRecord converts a legacy JSONL record into a canonical
// envelope with a stable ID. Re-imports of the same record are idempotent.
func EnvelopeFromRecord(rec Record) (*eventschema.Envelope, error) {
	if rec.Kind == "" {
		return nil, errors.New("domainevents: legacy record kind is empty")
	}
	if rec.At.IsZero() {
		return nil, errors.New("domainevents: legacy record timestamp is empty")
	}
	data := rec.Payload
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil, err
	}
	compact := append(json.RawMessage(nil), buf.Bytes()...)
	hashInput := make([]byte, 0, len(rec.Kind)+len(compact)+32)
	hashInput = append(hashInput, rec.Kind...)
	hashInput = append(hashInput, 0)
	hashInput = append(hashInput, rec.At.UTC().Format(time.RFC3339Nano)...)
	hashInput = append(hashInput, 0)
	hashInput = append(hashInput, compact...)
	stableID := uuid.NewSHA1(uuid.NameSpaceURL, append([]byte("tokenops/legacy-domain-event/"), hashInput...)).String()
	return &eventschema.Envelope{
		ID: stableID, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeDomain, Timestamp: rec.At.UTC(), Source: "domain_jsonl_migration",
		Association: associationFor(rec.Kind, compact),
		Payload:     &eventschema.DomainEvent{Kind: rec.Kind, Data: compact},
	}, nil
}

// associationFor adds only associations explicitly carried by a domain
// payload. WorkflowStarted.AgentID is an actor identity; workflow IDs are
// not promoted to Work or Execution because their ontology is not defined
// by the legacy event contract.
func associationFor(kind string, data json.RawMessage) eventschema.Association {
	if kind != KindWorkflowStarted {
		return eventschema.Association{}
	}
	var payload struct {
		AgentID      string `json:"AgentID"`
		AgentIDSnake string `json:"agent_id"`
		AgentIDCamel string `json:"agentId"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return eventschema.Association{}
	}
	actor := payload.AgentID
	if actor == "" {
		actor = payload.AgentIDSnake
	}
	if actor == "" {
		actor = payload.AgentIDCamel
	}
	return eventschema.Association{Actor: actor}
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
