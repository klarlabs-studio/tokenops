package eventschema

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewDomainEnvelope(t *testing.T) {
	at := time.Date(2026, 9, 24, 12, 30, 0, 0, time.FixedZone("test", 3600))
	env, err := NewDomainEnvelope("workflow.observed", map[string]string{"workflow": "wf-1"}, at, "workflows", Association{Actor: "agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if env.ID == "" || env.SchemaVersion != SchemaVersion || env.Type != EventTypeDomain || env.Source != "workflows" {
		t.Fatalf("envelope header = %+v", env)
	}
	if !env.Timestamp.Equal(at.UTC()) || env.Association.Actor != "agent-1" {
		t.Fatalf("timestamp/association = %s / %+v", env.Timestamp, env.Association)
	}
	var payload map[string]string
	if err := json.Unmarshal(env.Payload.(*DomainEvent).Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["workflow"] != "wf-1" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestNewDomainEnvelopeRequiresKind(t *testing.T) {
	if _, err := NewDomainEnvelope("", nil, time.Time{}, "", Association{}); err == nil {
		t.Fatal("empty kind accepted")
	}
}
