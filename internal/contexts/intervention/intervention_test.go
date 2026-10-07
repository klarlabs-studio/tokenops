package intervention_test

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

var t0 = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

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
		Assignment:   intervention.Randomised,
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
		// Randomised, because these exercise the conclusion logic, and
		// only an assigned comparison is allowed to reach one.
		Assignment:           intervention.Randomised,
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
		// Randomised, because these exercise the conclusion logic, and
		// only an assigned comparison is allowed to reach one.
		Assignment:           intervention.Randomised,
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
		// Randomised, because these exercise the conclusion logic, and
		// only an assigned comparison is allowed to reach one.
		Assignment:           intervention.Randomised,
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

// The confound that decides whether any of this is worth trusting.
//
// Splitting executions by whether an optimization happened to fire is
// observational, not experimental. Compression applies to large outputs;
// routing applies to turns a classifier thought were mechanical. The
// cohorts therefore differ in ways that have nothing to do with the
// intervention, and a difference between them cannot be attributed to
// it.
//
// TokenOps can still show the difference. What it must not do is call
// it an improvement — that is "tokens removed × nominal price" wearing a
// statistical costume.
func TestAnObservationalComparisonCannotConcludeItHelped(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Assignment:           intervention.Observational,
		Baseline:             measurement.Measured(5000, "tokenizer"),
		Intervention:         measurement.Measured(3000, "tokenizer"),
		Samples:              400,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 400},
		InterventionOutcomes: intervention.Outcomes{Achieved: 400},
		At:                   t0,
	})

	if v.Outcome == intervention.Improved {
		t.Error("an observational comparison claimed the intervention helped")
	}
	if v.Conclusive() {
		t.Error("an observational comparison reported itself conclusive")
	}
	// The measured difference is still reported — it is the reason to
	// run a real experiment, and hiding it would be its own dishonesty.
	if observed, ok := v.Observed.Amount(); !ok || observed != 2000 {
		t.Errorf("the difference was withheld: %v %v", observed, ok)
	}
	if !strings.Contains(v.Caveat, "not assigned") {
		t.Errorf("the caveat does not explain the confound: %q", v.Caveat)
	}
}

// Harm is different. An observational comparison showing the success
// rate collapsed is a reason to stop, even though it cannot prove
// causation: the cost of pausing an optimization that was innocent is
// far below the cost of continuing one that is not.
func TestObservationalHarmIsStillReported(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Assignment:           intervention.Observational,
		Baseline:             measurement.Measured(5000, "tokenizer"),
		Intervention:         measurement.Measured(3000, "tokenizer"),
		Samples:              400,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 380, NotAchieved: 20},
		InterventionOutcomes: intervention.Outcomes{Achieved: 200, NotAchieved: 200},
		At:                   t0,
	})

	if v.Outcome != intervention.Harmed {
		t.Errorf("outcome = %q; a collapsed success rate was not reported", v.Outcome)
	}
	if !strings.Contains(v.Caveat, "not assigned") {
		t.Errorf("the harm verdict does not disclose that it is observational: %q", v.Caveat)
	}
}

// A randomised comparison can conclude, because the cohorts were
// assigned rather than observed. This is the rung the experiment
// machinery has to reach before any saving is called proven.
func TestARandomisedComparisonCanConclude(t *testing.T) {
	v := intervention.Judge(intervention.Comparison{
		Assignment:           intervention.Randomised,
		Baseline:             measurement.Measured(5000, "tokenizer"),
		Intervention:         measurement.Measured(3000, "tokenizer"),
		Samples:              400,
		BaselineOutcomes:     intervention.Outcomes{Achieved: 400},
		InterventionOutcomes: intervention.Outcomes{Achieved: 400},
		At:                   t0,
	})

	if v.Outcome != intervention.Improved {
		t.Errorf("outcome = %q, want improved", v.Outcome)
	}
	if !v.Conclusive() {
		t.Error("a randomised comparison is not conclusive")
	}
}

// The zero Assignment is observational, so a caller that does not say
// how cohorts were formed gets the weaker reading rather than the
// stronger one.
func TestUnstatedAssignmentIsObservational(t *testing.T) {
	var c intervention.Comparison
	if c.Assignment != intervention.Observational {
		t.Errorf("zero assignment = %q, want observational", c.Assignment)
	}
}
