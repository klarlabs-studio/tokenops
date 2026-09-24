package domainevents

import (
	"encoding/json"
	"testing"
	"time"
)

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
