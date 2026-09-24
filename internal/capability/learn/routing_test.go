package learn

import (
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

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
