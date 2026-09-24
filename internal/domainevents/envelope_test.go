package domainevents

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type envelopeCollector struct {
	mu  sync.Mutex
	got []*eventschema.Envelope
}

func (c *envelopeCollector) Publish(env *eventschema.Envelope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, env)
}

func TestBridgeToEnvelopeBusForwardsCanonicalDomainEvent(t *testing.T) {
	source := &Bus{}
	target := &envelopeCollector{}
	sub := BridgeToEnvelopeBus(source, target, nil)
	if sub == nil {
		t.Fatal("bridge subscription is nil")
	}
	defer sub.Cancel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	source.Publish(BudgetExceeded{BudgetID: "daily", SpentUSD: 12, LimitUSD: 10, At: at})
	if len(target.got) != 1 {
		t.Fatalf("forwarded envelopes = %d, want 1", len(target.got))
	}
	payload, ok := target.got[0].Payload.(*eventschema.DomainEvent)
	if !ok || target.got[0].Type != eventschema.EventTypeDomain || payload.Kind != KindBudgetExceeded {
		t.Fatalf("forwarded envelope = %+v", target.got[0])
	}
}

func TestToEnvelopePreservesKindPayloadAndOccurrenceTime(t *testing.T) {
	at := time.Date(2026, 9, 24, 12, 30, 0, 0, time.FixedZone("test", 3600))
	env, err := ToEnvelope(WorkflowStarted{WorkflowID: "wf:1", AgentID: "agent:1", At: at})
	if err != nil {
		t.Fatal(err)
	}
	if env.ID == "" || env.SchemaVersion != eventschema.SchemaVersion || env.Type != eventschema.EventTypeDomain || env.Source != "domain_bus" {
		t.Fatalf("envelope header = %+v", env)
	}
	if !env.Timestamp.Equal(at.UTC()) {
		t.Fatalf("timestamp = %s, want %s", env.Timestamp, at.UTC())
	}
	if env.Association.Actor != "agent:1" || env.Association.Work != "" || env.Association.Execution != "" {
		t.Fatalf("association = %+v, want actor agent:1 only", env.Association)
	}
	payload, ok := env.Payload.(*eventschema.DomainEvent)
	if !ok || payload.Kind != KindWorkflowStarted {
		t.Fatalf("payload = %#v", env.Payload)
	}
	var data WorkflowStarted
	if err := json.Unmarshal(payload.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.WorkflowID != "wf:1" || data.AgentID != "agent:1" || !data.At.Equal(at) {
		t.Fatalf("domain data = %+v", data)
	}
}

func TestEnvelopeFromRecordRecoversExplicitActorAssociation(t *testing.T) {
	env, err := EnvelopeFromRecord(Record{
		Kind:    KindWorkflowStarted,
		At:      time.Date(2026, 9, 24, 12, 30, 0, 0, time.UTC),
		Payload: json.RawMessage(`{"WorkflowID":"wf:1","AgentID":"agent:1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Association.Actor != "agent:1" {
		t.Fatalf("migrated association = %+v", env.Association)
	}
}

func TestToEnvelopeUsesReplayTimestamp(t *testing.T) {
	at := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	env, err := ToEnvelope(NewReplayed(KindBudgetExceeded, at))
	if err != nil {
		t.Fatal(err)
	}
	if !env.Timestamp.Equal(at) {
		t.Fatalf("replayed timestamp = %s, want %s", env.Timestamp, at)
	}
	payload := env.Payload.(*eventschema.DomainEvent)
	if payload.Kind != KindBudgetExceeded || string(payload.Data) != "null" {
		t.Fatalf("replayed payload = %+v", payload)
	}
}

func TestToEnvelopeRejectsNil(t *testing.T) {
	if _, err := ToEnvelope(nil); err == nil {
		t.Fatal("nil event accepted")
	}
}
