package domainevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

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
	env, err := eventschema.NewDomainEnvelope(rec.Kind, compact, rec.At, "domain_jsonl_migration", associationFor(rec.Kind, compact))
	if err != nil {
		return nil, err
	}
	env.ID = stableID
	return env, nil
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
