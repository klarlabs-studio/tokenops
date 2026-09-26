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
		Metrics: map[string]MetricPair{"tokens": {Baseline: 100, Variant: 80}, "latency_ms": {Baseline: 100, Variant: 105}},
	}
	pairs := []Pair{good, good, good, good, good}
	got := Evaluate(Evidence{
		Eligible: 10, Pairs: pairs, Fingerprint: "current", Now: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []Guardrail{{Metric: "quality"}, {Metric: "latency_ms", MaxRegressionPct: 10}},
	})
	if got.Tier != TierSupported {
		t.Fatalf("tier = %s: %+v", got.Tier, got)
	}
	pairs[0].Variant = eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeSelfReport}
	if got := Evaluate(Evidence{
		Eligible: 10, Pairs: pairs, Fingerprint: "current", Now: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []Guardrail{{Metric: "quality"}, {Metric: "latency_ms", MaxRegressionPct: 10}},
	}); got.Tier == TierSupported {
		t.Fatalf("self-report promoted a belief: %+v", got)
	}
}

func TestUnknownGuardrailEvidenceBlocksPromotion(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	good := Pair{
		Baseline: eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		Variant:  eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		At:       now, Fingerprint: "current", Metrics: map[string]MetricPair{"tokens": {Baseline: 100, Variant: 80}},
	}
	pairs := []Pair{good, good, good, good, good}
	got := Evaluate(Evidence{
		Eligible: 10, Pairs: pairs, Fingerprint: "current", Now: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []Guardrail{{Metric: "quality"}, {Metric: "latency_ms", MaxRegressionPct: 10}},
	})
	if got.Tier != TierObserved || got.GuardrailUnknownPairs != 5 || got.Caveat != "unknown guardrail evidence blocks promotion" {
		t.Fatalf("unknown latency was treated as safe: %+v", got)
	}
}

func TestDeclaredGuardrailRegressionBlocksPromotion(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	bad := Pair{
		Baseline: eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		Variant:  eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		At:       now, Fingerprint: "current", Metrics: map[string]MetricPair{
			"tokens": {Baseline: 100, Variant: 80}, "latency_ms": {Baseline: 100, Variant: 120},
		},
	}
	pairs := []Pair{bad, bad, bad, bad, bad}
	got := Evaluate(Evidence{
		Eligible: 10, Pairs: pairs, Fingerprint: "current", Now: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []Guardrail{{Metric: "quality"}, {Metric: "latency_ms", MaxRegressionPct: 10}},
	})
	if got.Tier != TierObserved || got.GuardrailPassPairs != 0 {
		t.Fatalf("latency regression promoted: %+v", got)
	}
}

func TestLegacyEvidenceWithoutUtilityPolicyStaysObservational(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	good := Pair{
		Baseline:          eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		Variant:           eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		ResourceChangePct: 50, At: now, Fingerprint: "current",
	}
	got := Evaluate(Evidence{Eligible: 10, Pairs: []Pair{good, good, good, good, good}, Fingerprint: "current", Now: now})
	if got.Tier != TierObserved {
		t.Fatalf("legacy token-only evidence promoted: %+v", got)
	}
}

func TestEnvironmentDriftRetractsBelief(t *testing.T) {
	now := time.Unix(100000, 0).UTC()
	pair := Pair{
		Baseline:          eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		Variant:           eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman},
		ResourceChangePct: 20, At: now, Fingerprint: "old",
		Metrics: map[string]MetricPair{"tokens": {Baseline: 100, Variant: 80}},
	}
	got := Evaluate(Evidence{
		Eligible: 10, Pairs: []Pair{pair, pair, pair, pair, pair}, Fingerprint: "new", Now: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10, Guardrails: []Guardrail{{Metric: "quality"}},
	})
	if got.Tier != TierUnknown {
		t.Fatalf("stale belief survived fingerprint change: %+v", got)
	}
}
