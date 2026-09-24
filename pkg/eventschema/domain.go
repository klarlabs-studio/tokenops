package eventschema

import "encoding/json"

// DomainEvent represents a cross-context operational event whose detailed
// contract remains owned by its producer. Kind is a stable domain event name;
// Data is its JSON-encoded typed payload. This bridges legacy domain events
// into the common envelope without flattening them into untyped attributes.
type DomainEvent struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Type identifies this payload as a DomainEvent.
func (*DomainEvent) Type() EventType { return EventTypeDomain }
