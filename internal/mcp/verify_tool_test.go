package mcp

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// An agent asking "did compressing that output help?" is the caller
// most likely to take a number at face value and act on it. So the tool
// has to carry the refusal, not only the figure — a difference without
// its caveat is the "tokens removed × nominal price" claim handed
// straight to something that will repeat it.
func TestVerifyToolCarriesTheRefusalNotOnlyTheNumber(t *testing.T) {
	payload := verifyPayload(verify.Report{
		BaselineCount: 20, InterventionCount: 18,
		Comparison: intervention.Comparison{Assignment: intervention.Observational},
		Verdict: intervention.Verdict{
			Observed: measurement.Measured(1800, "sqlite_events"),
			Samples:  18,
			Caveat:   "the cohorts were not assigned",
		},
	})

	if payload.Observational != true {
		t.Error("an observational comparison did not say so")
	}
	if payload.Proven {
		t.Error("an observational comparison was reported as proven")
	}
	if payload.Caveat == "" {
		t.Error("the caveat did not reach the agent")
	}
	if payload.Reading == "" {
		t.Error("no reading was given")
	}
}

func TestVerifyToolCarriesOutcomeCohortRates(t *testing.T) {
	base := measurement.Measured(70, "outcome_events")
	with := measurement.Measured(75, "outcome_events")
	payload := verifyPayload(verify.Report{
		BaselineOutcomes:     verify.OutcomeSummary{Achieved: 7, NotAchieved: 3, SuccessRate: base},
		InterventionOutcomes: verify.OutcomeSummary{Achieved: 7, Partial: 1, NotAchieved: 2, SuccessRate: with},
	})
	if got, ok := payload.BaselineOutcomes.SuccessRate.Amount(); !ok || got != 70 {
		t.Fatalf("baseline success rate = %v, known=%v", got, ok)
	}
	if got, ok := payload.InterventionOutcomes.SuccessRate.Amount(); !ok || got != 75 {
		t.Fatalf("intervention success rate = %v, known=%v", got, ok)
	}
}

// The CLI and the tool must describe one verdict the same way. Two
// surfaces wording a judgement differently is how an operator and their
// agent come to disagree about what happened.
func TestVerifyToolAndCLIShareOneReading(t *testing.T) {
	report := verify.Report{
		Verdict: intervention.Verdict{Outcome: intervention.Harmed, Samples: 30},
	}
	if verifyPayload(report).Reading != report.Reading() {
		t.Error("the tool reworded the capability's reading")
	}
}
