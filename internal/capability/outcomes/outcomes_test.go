package outcomes

import (
	"testing"
	"time"

	toolcoach "go.klarlabs.de/tokenops/internal/contexts/coaching/tools"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestFromToolEventsUsesVerifierAfterLastEdit(t *testing.T) {
	t0 := time.Unix(100, 0).UTC()
	events := []toolcoach.ToolEvent{
		{Timestamp: t0, ToolUseID: "old", Name: "Bash", RawCommand: "go test ./..."},
		{Timestamp: t0.Add(time.Second), ToolUseID: "old", IsResult: true},
		{Timestamp: t0.Add(2 * time.Second), ToolUseID: "edit", Name: "Edit"},
		{Timestamp: t0.Add(3 * time.Second), ToolUseID: "new", Name: "Bash", RawCommand: "go test ./..."},
		{Timestamp: t0.Add(4 * time.Second), ToolUseID: "new", IsResult: true, IsError: true},
	}
	env, ok := FromToolEvents("exec:1", "decision:1", events)
	if !ok {
		t.Fatal("verifier was not observed")
	}
	out := env.Payload.(*eventschema.OutcomeEvent)
	if out.Result != eventschema.OutcomeNotAchieved || out.Assessment != eventschema.OutcomeVerification {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestResolvePrefersHumanAndRefusesEqualConflict(t *testing.T) {
	verification := Event(Record{Result: eventschema.OutcomeNotAchieved, Assessment: eventschema.OutcomeVerification})
	human := Event(Record{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman})
	if got := Resolve([]*eventschema.Envelope{verification, human}); got.Result != eventschema.OutcomeAchieved {
		t.Fatalf("human assessment did not win: %+v", got)
	}
	otherHuman := Event(Record{Result: eventschema.OutcomeNotAchieved, Assessment: eventschema.OutcomeHuman})
	if got := Resolve([]*eventschema.Envelope{human, otherHuman}); got.Result != eventschema.OutcomePartial {
		t.Fatalf("conflict was hidden: %+v", got)
	}
}

func TestCorrelateDecisionLifecycleCopiesInterventionAndExperiment(t *testing.T) {
	env := Event(Record{ExecutionID: "exec:1", DecisionID: "decision:1", Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman})
	history := []*eventschema.Envelope{
		{Correlation: eventschema.Correlation{Decision: "decision:other", Intervention: "intervention:wrong", Experiment: "experiment:wrong"}},
		{Correlation: eventschema.Correlation{Decision: "decision:1", Intervention: "intervention:1"}},
		{Correlation: eventschema.Correlation{Decision: "decision:1", Experiment: "experiment:1"}},
	}
	CorrelateDecisionLifecycle(env, history)
	if env.Correlation.Decision != "decision:1" || env.Correlation.Intervention != "intervention:1" || env.Correlation.Experiment != "experiment:1" {
		t.Fatalf("outcome correlation = %+v", env.Correlation)
	}
}
