package eventschema

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// NewDomainEnvelope creates a canonical envelope for a producer-owned domain
// event body. The body remains JSON so consumers can evolve independently.
func NewDomainEnvelope(kind string, body any, at time.Time, source string, association Association) (*Envelope, error) {
	if kind == "" {
		return nil, errors.New("eventschema: domain event kind is empty")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if at.IsZero() {
		at = time.Now()
	}
	return &Envelope{
		ID: uuid.NewString(), SchemaVersion: SchemaVersion,
		Type: EventTypeDomain, Timestamp: at.UTC(), Source: source,
		Association: association,
		Payload:     &DomainEvent{Kind: kind, Data: data},
	}, nil
}
