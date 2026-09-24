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

func TestFindRoutingAggregatesMatchingTrialsToReachTrusted(t *testing.T) {
	now := time.Unix(200000, 0).UTC()
	reader := experimentReader{events: map[string][]*eventschema.Envelope{}}
	for trial := 1; trial <= 2; trial++ {
		id := fmt.Sprintf("experiment:%d", trial)
		reader.states = append(reader.states, experiments.State{ID: id, Fingerprint: "fp", EndsAt: now.Add(time.Duration(trial) * time.Hour)})
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
