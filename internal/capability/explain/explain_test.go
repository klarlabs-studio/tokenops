package explain

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestBuildUsesRecordedRationaleAndStrongestOutcome(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	events := []*eventschema.Envelope{
		{ID: "d1", Timestamp: now, Correlation: eventschema.Correlation{Decision: "decision:1"}, Payload: &eventschema.DecisionEvent{Kind: "model_route", Stage: eventschema.DecisionStageShadow, Rationale: "measured pressure"}},
		{ID: "d2", Timestamp: now.Add(time.Second), Correlation: eventschema.Correlation{Decision: "decision:1"}, Payload: &eventschema.DecisionEvent{Kind: "model_route", Stage: eventschema.DecisionStageApplied, Rationale: "operator enrolled"}},
		{ID: "o1", Timestamp: now.Add(2 * time.Second), Correlation: eventschema.Correlation{Decision: "decision:1"}, Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman}},
	}
	got, ok := Build("decision:1", events)
	if !ok || got.Stage != eventschema.DecisionStageApplied || got.Rationale != "operator enrolled" {
		t.Fatalf("report = %+v", got)
	}
	if got.Outcome.Result != eventschema.OutcomeAchieved || len(got.History) != 2 {
		t.Fatalf("history/outcome = %+v", got)
	}
}
