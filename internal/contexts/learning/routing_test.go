package learning

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestEvidenceTiersRequireFreshStrongMatchedOutcomes(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	good := Pair{
		Baseline:          eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeVerification},
		Variant:           eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		ResourceChangePct: 15, At: now, Fingerprint: "current",
	}
	pairs := []Pair{good, good, good, good, good}
	got := Evaluate(Evidence{Eligible: 10, Pairs: pairs, Fingerprint: "current", Now: now})
	if got.Tier != TierSupported {
		t.Fatalf("tier = %s: %+v", got.Tier, got)
	}
	pairs[0].Variant = eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeSelfReport}
	if got := Evaluate(Evidence{Eligible: 10, Pairs: pairs, Fingerprint: "current", Now: now}); got.Tier == TierSupported {
		t.Fatalf("self-report promoted a belief: %+v", got)
	}
}

func TestEnvironmentDriftRetractsBelief(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	pair := Pair{
		Baseline:          eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		Variant:           eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		ResourceChangePct: 20, At: now, Fingerprint: "old",
	}
	got := Evaluate(Evidence{Eligible: 10, Pairs: []Pair{pair, pair, pair, pair, pair}, Fingerprint: "new", Now: now})
	if got.Tier != TierUnknown {
		t.Fatalf("stale belief survived fingerprint change: %+v", got)
	}
}
