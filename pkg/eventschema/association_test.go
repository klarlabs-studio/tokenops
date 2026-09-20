package eventschema_test

import (
	"encoding/json"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The envelope carried workflow_id, agent_id, session_id and user_id —
// attribution invented before the ontology existed. None of them says
// which *attempt* at which *goal* produced the event, so the Work,
// Execution and Actor introduced in Phase 3 had nothing to attach to and
// no event could be rolled up to a goal a person would recognise.
func TestAnEnvelopeCanBeAssociatedWithWorkAndAnAttempt(t *testing.T) {
	env := eventschema.Envelope{
		ID:   "e1",
		Type: eventschema.EventTypePrompt,
		Association: eventschema.Association{
			Work:      "work:t1",
			Execution: "exec:t1",
			Actor:     "session:abc",
		},
	}

	if !env.Associated() {
		t.Error("an envelope with an association reports itself unassociated")
	}
	if env.Association.Work != "work:t1" {
		t.Errorf("work = %q", env.Association.Work)
	}
}

// Most events will carry no association for a long time — nothing
// reconstructs work from live traffic yet. An unassociated envelope must
// say so rather than appearing to belong to an empty work.
func TestAnUnassociatedEnvelopeSaysSo(t *testing.T) {
	var env eventschema.Envelope
	if env.Associated() {
		t.Error("an envelope with no association reports itself associated")
	}
}

// Partial association is real: the proxy may know the actor from a
// header long before anything knows the goal. Requiring all three would
// mean discarding the part that is known.
func TestPartialAssociationIsAllowed(t *testing.T) {
	env := eventschema.Envelope{
		Association: eventschema.Association{Actor: "session:abc"},
	}
	if !env.Associated() {
		t.Error("an envelope with only an actor reports itself unassociated")
	}
	if env.Association.Work != "" {
		t.Errorf("a work id was invented: %q", env.Association.Work)
	}
}

// The association is omitted from the wire form when empty, so every
// envelope written before this existed serialises byte-identically.
func TestEmptyAssociationIsOmitted(t *testing.T) {
	body, err := json.Marshal(eventschema.Envelope{ID: "e1", Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := back["association"]; present {
		t.Errorf("an empty association was serialised: %s", body)
	}
}

// Envelopes written before the field existed must read back unchanged,
// and unassociated — not as belonging to some default work.
func TestPriorEnvelopesReadBackUnassociated(t *testing.T) {
	const prior = `{"id":"e1","schema_version":"1.0.0","type":"prompt",
		"timestamp":"2026-09-20T09:00:00Z","source":"proxy"}`
	var env eventschema.Envelope
	if err := json.Unmarshal([]byte(prior), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Associated() {
		t.Error("an envelope from before the field existed reads as associated")
	}
	if env.ID != "e1" || env.Source != "proxy" {
		t.Errorf("the envelope changed: %+v", env)
	}
}

func TestAssociationRoundTrips(t *testing.T) {
	orig := eventschema.Envelope{
		ID: "e1", Type: eventschema.EventTypePrompt,
		Association: eventschema.Association{Work: "w", Execution: "x", Actor: "a"},
	}
	body, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back eventschema.Envelope
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Association != orig.Association {
		t.Errorf("association = %+v, want %+v", back.Association, orig.Association)
	}
}
