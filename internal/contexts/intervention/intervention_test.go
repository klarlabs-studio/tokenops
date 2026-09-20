package intervention_test

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/work"
)

var t0 = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

// An intervention is something TokenOps did, or proposed doing, to
// change how work is performed. It needs an identity because nothing
// correlates the four stages today: OptimizationEvent carries no
// recommendation id, routing approvals record proposed and decided and
// no result, and eventschema's coaching Decision is documented as
// recording adoption while nothing anywhere writes it.
func TestAnInterventionHasAnIdentityAndATarget(t *testing.T) {
	i := intervention.Propose("i1", intervention.KindModelRoute, "exec-1", t0)

	if i.ID != "i1" {
		t.Errorf("id = %q", i.ID)
	}
	if i.Target != work.ID("exec-1") {
		t.Errorf("target = %q", i.Target)
	}
	if i.Decision != intervention.Proposed {
		t.Errorf("a fresh intervention is %q, want proposed", i.Decision)
	}
}

// The invariant the read guard already enforces by hand and which this
// generalises: an intervention that was only observed must never be
// credited with an effect. Crediting it reports uplift the system did
// not deliver.
func TestAnObservedInterventionDeliversNothing(t *testing.T) {
	i := intervention.Propose("i1", intervention.KindReadGuard, "exec-1", t0).
		Observed(t0.Add(time.Second))

	if i.Delivered() {
		t.Error("an observe-mode intervention reports itself delivered")
	}
	// It is still worth recording: the counterfactual is how an operator
	// decides whether to turn the thing on.
	if i.Decision != intervention.Observed {
		t.Errorf("decision = %q", i.Decision)
	}
}

// An applied intervention did change what happened, and only those may
// be credited.
func TestAnAppliedInterventionIsDelivered(t *testing.T) {
	i := intervention.Propose("i1", intervention.KindPromptCompress, "exec-1", t0).
		Applied(t0.Add(time.Second))

	if !i.Delivered() {
		t.Error("an applied intervention does not report itself delivered")
	}
}

// A rejected proposal is neither delivered nor forgotten: a proposal an
// operator keeps declining is a signal about the proposer.
func TestARejectedInterventionIsRecorded(t *testing.T) {
	i := intervention.Propose("i1", intervention.KindModelRoute, "exec-1", t0).
		Rejected(t0.Add(time.Minute), "the cheaper model failed this task last week")

	if i.Delivered() {
		t.Error("a rejected intervention reports itself delivered")
	}
	if i.Reason == "" {
		t.Error("the rejection reason was dropped")
	}
}

// The core claim this phase exists to refuse: tokens removed times
// nominal price is not a proven saving. An intervention may cut tokens
// while raising cost, breaking caching, causing retries, adding latency
// or degrading the outcome.
func TestAClaimedSavingIsNotAProvenSaving(t *testing.T) {
	i := intervention.Propose("i1", intervention.KindPromptCompress, "exec-1", t0).
		Applied(t0).
		Claiming(measurement.Estimated(1200, "byte_delta", "bytes/4 heuristic"))

	if i.Proven() {
		t.Error("a claim with no verification reports itself proven")
	}
	claimed, ok := i.Claimed.Amount()
	if !ok || claimed != 1200 {
		t.Errorf("the claim was lost: %v %v", claimed, ok)
	}
}

// A verdict is what a comparison against a baseline concluded. Only a
// verdict makes a claim proven.
func TestAVerifiedInterventionIsProven(t *testing.T) {
	i := intervention.Propose("i1", intervention.KindPromptCompress, "exec-1", t0).
		Applied(t0).
		Claiming(measurement.Estimated(1200, "byte_delta", "bytes/4")).
		Verified(intervention.Verdict{
			Outcome:  intervention.Improved,
			Observed: measurement.Measured(900, "tokenizer"),
			Samples:  40,
			At:       t0.Add(time.Hour),
		})

	if !i.Proven() {
		t.Error("a verified intervention does not report itself proven")
	}
	// The proven figure is what was observed, not what was claimed.
	observed, ok := i.Verdict.Observed.Amount()
	if !ok || observed != 900 {
		t.Errorf("observed = %v, want the measured figure not the claim", observed)
	}
}

// An intervention can be harmful, and the model has to be able to say
// so. A system that can only record wins will only ever report wins.
func TestAnInterventionCanBeHarmful(t *testing.T) {
	v := intervention.Verdict{
		Outcome:  intervention.Harmed,
		Observed: measurement.Measured(-300, "tokenizer"),
		Samples:  40,
		Caveat:   "compression broke the prompt cache; input tokens rose",
		At:       t0,
	}
	if v.Helped() {
		t.Error("a harmful verdict reports that it helped")
	}
	if !v.Conclusive() {
		t.Error("a harmful verdict is not conclusive")
	}
}

// Not enough evidence is its own answer. Collapsing it into "no effect"
// is how an intervention nobody measured becomes an intervention that
// demonstrably did nothing — and the two lead to opposite decisions.
func TestInsufficientEvidenceIsNotNoEffect(t *testing.T) {
	v := intervention.Verdict{Outcome: intervention.Inconclusive, Samples: 2, At: t0}

	if v.Outcome == intervention.NoEffect {
		t.Error("insufficient evidence was recorded as no effect")
	}
	if v.Conclusive() {
		t.Error("an inconclusive verdict reports itself conclusive")
	}
	if v.Helped() {
		t.Error("an inconclusive verdict claims it helped")
	}
}

// The zero Verdict is inconclusive, so an unverified intervention does
// not read as one that was measured and found neutral.
func TestZeroVerdictIsInconclusive(t *testing.T) {
	var v intervention.Verdict
	if v.Conclusive() {
		t.Error("the zero Verdict reports itself conclusive")
	}
	if v.Outcome != intervention.Inconclusive {
		t.Errorf("zero outcome = %q", v.Outcome)
	}
}

// An intervention that cut tokens while making the work fail is not a
// win, and the verdict must reflect the outcome, not only the
// consumption. This is the multi-objective rule the intent asks for.
func TestCheaperButWorseIsNotAWin(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Baseline:     measurement.Measured(5000, "tokenizer"),
		Intervention: measurement.Measured(3000, "tokenizer"),
		Samples:      40,
		BaselineOutcomes: intervention.Outcomes{
			Achieved: 38, NotAchieved: 2,
		},
		InterventionOutcomes: intervention.Outcomes{
			Achieved: 20, NotAchieved: 20,
		},
		At: t0,
	})

	if v.Outcome != intervention.Harmed {
		t.Errorf("outcome = %q; halving tokens while halving the success "+
			"rate was not reported as harm", v.Outcome)
	}
	if v.Caveat == "" {
		t.Error("nothing explained why a cheaper result was judged harmful")
	}
}

// A genuine win: fewer tokens, outcomes held.
func TestCheaperAndNoWorseIsAWin(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Baseline:             measurement.Measured(5000, "tokenizer"),
		Intervention:         measurement.Measured(3000, "tokenizer"),
		Samples:              40,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 38, NotAchieved: 2},
		InterventionOutcomes: intervention.Outcomes{Achieved: 38, NotAchieved: 2},
		At:                   t0,
	})

	if v.Outcome != intervention.Improved {
		t.Errorf("outcome = %q, want improved", v.Outcome)
	}
	observed, ok := v.Observed.Amount()
	if !ok || observed != 2000 {
		t.Errorf("observed saving = %v, want 2000", observed)
	}
}

// Too few samples cannot conclude anything, however good the numbers
// look. This is the guard against declaring victory on one run.
func TestTooFewSamplesCannotConclude(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Baseline:             measurement.Measured(5000, "tokenizer"),
		Intervention:         measurement.Measured(1, "tokenizer"),
		Samples:              1,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 1},
		InterventionOutcomes: intervention.Outcomes{Achieved: 1},
		At:                   t0,
	})

	if v.Conclusive() {
		t.Errorf("one sample produced a conclusive verdict: %+v", v)
	}
	if v.Outcome != intervention.Inconclusive {
		t.Errorf("outcome = %q", v.Outcome)
	}
}

// A comparison against an unknown baseline cannot conclude. This is the
// Phase 1 invariant reaching its natural consumer: unknown minus
// something is not a saving of something.
func TestAnUnknownBaselineCannotConclude(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Baseline:             measurement.Unknown("nothing priced the baseline"),
		Intervention:         measurement.Measured(3000, "tokenizer"),
		Samples:              40,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 40},
		InterventionOutcomes: intervention.Outcomes{Achieved: 40},
		At:                   t0,
	})

	if v.Conclusive() {
		t.Error("an unknown baseline produced a conclusive verdict")
	}
	if v.Caveat == "" {
		t.Error("nothing explained that the baseline was unknown")
	}
}

// No change worth calling a change is "no effect" — a real, conclusive
// answer, and the one an operator needs before turning something off.
func TestNoMeaningfulChangeIsNoEffect(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Baseline:             measurement.Measured(5000, "tokenizer"),
		Intervention:         measurement.Measured(4995, "tokenizer"),
		Samples:              40,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 40},
		InterventionOutcomes: intervention.Outcomes{Achieved: 40},
		At:                   t0,
	})

	if v.Outcome != intervention.NoEffect {
		t.Errorf("outcome = %q, want no_effect for a 0.1%% difference", v.Outcome)
	}
	if !v.Conclusive() {
		t.Error("a measured no-effect is not conclusive")
	}
}

// An intervention that made work more expensive is harm even when the
// outcomes held.
func TestMoreExpensiveIsHarm(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Baseline:             measurement.Measured(3000, "tokenizer"),
		Intervention:         measurement.Measured(5000, "tokenizer"),
		Samples:              40,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 40},
		InterventionOutcomes: intervention.Outcomes{Achieved: 40},
		At:                   t0,
	})

	if v.Outcome != intervention.Harmed {
		t.Errorf("outcome = %q, want harmed", v.Outcome)
	}
}

// Outcomes report their own success rate, and an empty set has none
// rather than a rate of zero — which would make "we measured nothing"
// look like total failure.
func TestEmptyOutcomesHaveNoRate(t *testing.T) {
	var o intervention.Outcomes
	if _, ok := o.SuccessRate(); ok {
		t.Error("an empty outcome set reported a success rate")
	}
	rate, ok := intervention.Outcomes{Achieved: 3, NotAchieved: 1}.SuccessRate()
	if !ok || rate != 0.75 {
		t.Errorf("rate = %v, ok = %v, want 0.75", rate, ok)
	}
}
