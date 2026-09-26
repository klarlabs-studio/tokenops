package learn

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type experimentReader struct {
	states []experiments.State
	events map[string][]*eventschema.Envelope
}

func (r experimentReader) States(context.Context, time.Time) ([]experiments.State, error) {
	return r.states, nil
}

func (r experimentReader) Events(_ context.Context, id string) ([]*eventschema.Envelope, error) {
	return r.events[id], nil
}

func TestRoutingPromotesOnlyOutcomeLinkedMatchedEvidence(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	var events []*eventschema.Envelope
	events = append(events, &eventschema.Envelope{Timestamp: now, Payload: &eventschema.ExperimentEvent{
		Stage: eventschema.ExperimentStarted, ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []eventschema.ExperimentGuardrail{{Metric: "quality"}},
	}})
	for pair := 1; pair <= 5; pair++ {
		for _, arm := range []string{"baseline", "variant"} {
			decision := fmt.Sprintf("d:%d:%s", pair, arm)
			events = append(events,
				&eventschema.Envelope{Timestamp: now, Correlation: eventschema.Correlation{Decision: decision}, Attributes: map[string]string{"tokenops.experiment.pair": fmt.Sprint(pair), "tokenops.experiment.arm": arm}, Payload: &eventschema.DecisionEvent{}},
				&eventschema.Envelope{Timestamp: now, Correlation: eventschema.Correlation{Decision: decision}, Payload: &eventschema.PromptEvent{TotalTokens: map[string]int64{"baseline": 100, "variant": 80}[arm], TokenSource: eventschema.TokenSourceCounted}},
				&eventschema.Envelope{Timestamp: now, Correlation: eventschema.Correlation{Decision: decision}, Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeVerification}},
			)
		}
	}
	got := Routing(events, "fp", now)
	if got.Tier != learning.TierSupported || got.MedianChangePct != 20 {
		t.Fatalf("belief = %+v", got)
	}
}

func TestRoutingAggregatesMultiCallEvidenceByExecution(t *testing.T) {
	now := time.Unix(150000, 0).UTC()
	experiment := "experiment:multi"
	events := make([]*eventschema.Envelope, 0, 11)
	events = append(events, &eventschema.Envelope{
		Timestamp: now, Correlation: eventschema.Correlation{Experiment: experiment},
		Payload: &eventschema.ExperimentEvent{
			Stage: eventschema.ExperimentStarted, ObjectiveMetric: "tokens", MinImprovementPct: 10,
			Guardrails: []eventschema.ExperimentGuardrail{{Metric: "quality"}},
		},
	})
	for _, tc := range []struct {
		execution, arm string
		perCall        int64
	}{
		{execution: "exec:baseline", arm: "baseline", perCall: 100},
		{execution: "exec:variant", arm: "variant", perCall: 40},
	} {
		firstDecision := "decision:" + tc.arm + ":1"
		corr := eventschema.Correlation{Decision: firstDecision, Experiment: experiment}
		association := eventschema.Association{Execution: tc.execution}
		events = append(events,
			&eventschema.Envelope{Timestamp: now, Association: association, Correlation: corr, Attributes: map[string]string{"tokenops.experiment.pair": "1", "tokenops.experiment.arm": tc.arm}, Payload: &eventschema.DecisionEvent{}},
			&eventschema.Envelope{Timestamp: now, Association: association, Correlation: corr, Payload: &eventschema.PromptEvent{TotalTokens: tc.perCall, TokenSource: eventschema.TokenSourceCounted}},
			// A later call may have a new decision or no decision correlation;
			// execution identity must keep both in the same arm.
			&eventschema.Envelope{Timestamp: now, Association: association, Correlation: eventschema.Correlation{Decision: "decision:" + tc.arm + ":2", Experiment: experiment}, Attributes: map[string]string{"tokenops.experiment.pair": "1", "tokenops.experiment.arm": tc.arm}, Payload: &eventschema.DecisionEvent{}},
			&eventschema.Envelope{Timestamp: now, Association: association, Payload: &eventschema.PromptEvent{TotalTokens: tc.perCall, TokenSource: eventschema.TokenSourceCounted}},
			&eventschema.Envelope{Timestamp: now, Association: association, Correlation: corr, Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman}},
		)
	}
	got := Routing(events, "fp", now)
	if got.CompletedPairs != 1 || got.StrongCoverage != 1 || got.MedianChangePct != 60 {
		t.Fatalf("multi-call belief = %+v", got)
	}
}

func TestFindRoutingAggregatesMatchingTrialsToReachTrusted(t *testing.T) {
	now := time.Unix(200000, 0).UTC()
	reader := experimentReader{events: map[string][]*eventschema.Envelope{}}
	for trial := 1; trial <= 2; trial++ {
		id := fmt.Sprintf("experiment:%d", trial)
		reader.states = append(reader.states, experiments.State{
			ID: id, Fingerprint: "fp", EndsAt: now.Add(time.Duration(trial) * time.Hour),
			ObjectiveMetric: "tokens", MinImprovementPct: 10,
			Guardrails: []eventschema.ExperimentGuardrail{{Metric: "quality"}},
		})
		reader.events[id] = append(reader.events[id], &eventschema.Envelope{
			Timestamp: now, Correlation: eventschema.Correlation{Experiment: id},
			Payload: &eventschema.ExperimentEvent{
				Stage: eventschema.ExperimentStarted, ObjectiveMetric: "tokens", MinImprovementPct: 10,
				Guardrails: []eventschema.ExperimentGuardrail{{Metric: "quality"}},
			},
		})
		for pair := 1; pair <= 10; pair++ {
			for _, arm := range []string{"baseline", "variant"} {
				decision := fmt.Sprintf("d:%d:%d:%s", trial, pair, arm)
				corr := eventschema.Correlation{Decision: decision, Experiment: id}
				reader.events[id] = append(reader.events[id],
					&eventschema.Envelope{Timestamp: now, Correlation: corr, Attributes: map[string]string{"tokenops.experiment.pair": fmt.Sprint(pair), "tokenops.experiment.arm": arm}, Payload: &eventschema.DecisionEvent{}},
					&eventschema.Envelope{Timestamp: now, Correlation: corr, Payload: &eventschema.PromptEvent{TotalTokens: map[string]int64{"baseline": 100, "variant": 80}[arm], TokenSource: eventschema.TokenSourceCounted}},
					&eventschema.Envelope{Timestamp: now, Correlation: corr, Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman}},
				)
			}
		}
	}
	got, found, err := FindRouting(context.Background(), reader, "fp", now)
	if err != nil || !found || got.Tier != learning.TierTrusted || got.CompletedPairs != 20 {
		t.Fatalf("belief = %+v found=%v err=%v", got, found, err)
	}
}

func TestFindRoutingDoesNotPoolDifferentUtilityPolicies(t *testing.T) {
	now := time.Unix(300000, 0).UTC()
	reader := experimentReader{events: map[string][]*eventschema.Envelope{}}
	for trial := 1; trial <= 2; trial++ {
		id := fmt.Sprintf("experiment:%d", trial)
		threshold := float64(10 * trial)
		policy := []eventschema.ExperimentGuardrail{{Metric: "quality"}}
		reader.states = append(reader.states, experiments.State{
			ID: id, Fingerprint: "fp", EndsAt: now.Add(time.Duration(trial) * time.Hour),
			ObjectiveMetric: "tokens", MinImprovementPct: threshold, Guardrails: policy,
		})
		reader.events[id] = append(reader.events[id], &eventschema.Envelope{
			Timestamp: now, Correlation: eventschema.Correlation{Experiment: id},
			Payload: &eventschema.ExperimentEvent{
				Stage: eventschema.ExperimentStarted, ObjectiveMetric: "tokens", MinImprovementPct: threshold,
				Guardrails: policy,
			},
		})
		for pair := 1; pair <= 10; pair++ {
			for _, arm := range []string{"baseline", "variant"} {
				decision := fmt.Sprintf("d:%d:%d:%s", trial, pair, arm)
				corr := eventschema.Correlation{Decision: decision, Experiment: id}
				reader.events[id] = append(reader.events[id],
					&eventschema.Envelope{Timestamp: now, Correlation: corr, Attributes: map[string]string{"tokenops.experiment.pair": fmt.Sprint(pair), "tokenops.experiment.arm": arm}, Payload: &eventschema.DecisionEvent{}},
					&eventschema.Envelope{Timestamp: now, Correlation: corr, Payload: &eventschema.PromptEvent{TotalTokens: map[string]int64{"baseline": 100, "variant": 80}[arm], TokenSource: eventschema.TokenSourceCounted}},
					&eventschema.Envelope{Timestamp: now, Correlation: corr, Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman}},
				)
			}
		}
	}
	got, found, err := FindRouting(context.Background(), reader, "fp", now)
	if err != nil || !found || got.Tier != learning.TierSupported || got.CompletedPairs != 10 {
		t.Fatalf("different trial policies were pooled: %+v found=%v err=%v", got, found, err)
	}
}
