// Package intervention represents something TokenOps did, or proposed
// doing, to change how work is performed — and whether it helped.
//
// The claim this package exists to refuse is "tokens removed × nominal
// price = money saved". An intervention may cut tokens while raising
// cost, breaking the prompt cache, causing retries, adding latency or
// degrading the outcome. None of those are visible in a token delta, and
// a system that reports the delta as a saving will report savings it did
// not deliver.
//
// # Nothing correlated the stages
//
// Propose, apply, observe and verify lived in four disconnected stores
// with no identity joining them: OptimizationEvent carries no
// recommendation id, routing approvals record proposed and decided and
// never a result, and eventschema's coaching Decision is documented as
// recording adoption while nothing anywhere writes it. An Intervention
// is that missing identity.
//
// # Two existing pieces had the right shape
//
// The read guard measures an intervention it actually performed and
// deliberately excludes observe-mode would_block, because crediting it
// would report uplift the guard did not deliver. fmt learn mines
// compression records against later recover events as a "did this harm
// the agent" signal, and never changes runtime behaviour on its own.
// Delivered and Verdict generalise those two instincts.
package intervention

import (
	"fmt"
	"math"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/work"
)

// ID identifies an intervention. It is the correlation key the four
// stages never had.
type ID string

// Kind names what was changed. Open enough for new optimizers, closed
// enough that a surface can group by it.
type Kind string

const (
	// KindModelRoute — run the work on a different model.
	KindModelRoute Kind = "model_route"
	// KindPromptCompress — send a smaller prompt.
	KindPromptCompress Kind = "prompt_compress"
	// KindContextTrim — send less history.
	KindContextTrim Kind = "context_trim"
	// KindReadGuard — refuse a redundant read.
	KindReadGuard Kind = "read_guard"
	// KindCommandFmt — compress a command's output before an agent sees
	// it.
	KindCommandFmt Kind = "command_fmt"
	// KindCoaching — advise the actor rather than change the request.
	KindCoaching Kind = "coaching"
	// KindCache — serve from cache instead of the provider.
	KindCache Kind = "cache"
)

// Decision is what became of an intervention.
type Decision string

const (
	// Proposed — suggested, nothing done yet. The zero Decision.
	Proposed Decision = ""
	// Observed — the system decided it *would* act but did not, because
	// it is running in observe mode. Recorded because the
	// counterfactual is how an operator decides whether to switch the
	// thing on; never credited with an effect.
	Observed Decision = "observed"
	// Applied — the intervention changed what actually happened.
	Applied Decision = "applied"
	// Rejected — a person or a policy declined it. Worth keeping: a
	// proposal that keeps being declined is a signal about the proposer.
	Rejected Decision = "rejected"
	// Expired — nobody decided in time.
	Expired Decision = "expired"
)

// Intervention is one attempt to change how work is performed.
type Intervention struct {
	ID   ID   `json:"id"`
	Kind Kind `json:"kind"`
	// Target is the execution or work this acted on.
	Target work.ID `json:"target"`

	Decision   Decision  `json:"decision,omitempty"`
	ProposedAt time.Time `json:"proposed_at"`
	DecidedAt  time.Time `json:"decided_at,omitzero"`
	// Reason explains a rejection, or why the proposal was made.
	Reason string `json:"reason,omitempty"`

	// Claimed is the effect the proposer expected — usually an estimate,
	// and never by itself evidence of anything.
	Claimed measurement.Value `json:"claimed,omitzero"`
	// Verdict is what a comparison against a baseline concluded. The
	// zero Verdict is inconclusive, so an unverified intervention does
	// not read as one that was measured and found neutral.
	Verdict Verdict `json:"verdict,omitzero"`
}

// Propose records an intervention that has been suggested.
func Propose(id ID, kind Kind, target work.ID, at time.Time) Intervention {
	return Intervention{ID: id, Kind: kind, Target: target, ProposedAt: at}
}

// Observed marks an intervention the system would have made but did not,
// because it is running in observe mode. Returns a copy.
func (i Intervention) Observed(at time.Time) Intervention {
	i.Decision, i.DecidedAt = Observed, at
	return i
}

// Applied marks an intervention that changed what happened.
func (i Intervention) Applied(at time.Time) Intervention {
	i.Decision, i.DecidedAt = Applied, at
	return i
}

// Rejected marks a declined proposal and keeps the reason.
func (i Intervention) Rejected(at time.Time, why string) Intervention {
	i.Decision, i.DecidedAt, i.Reason = Rejected, at, why
	return i
}

// Claiming attaches the effect the proposer expected.
func (i Intervention) Claiming(v measurement.Value) Intervention {
	i.Claimed = v
	return i
}

// Verified attaches what a comparison concluded.
func (i Intervention) Verified(v Verdict) Intervention {
	i.Verdict = v
	return i
}

// Delivered reports whether this intervention actually changed what
// happened.
//
// Only Applied qualifies. An observe-mode decision is a counterfactual,
// and crediting it reports uplift the system did not deliver — the
// mistake the read guard avoids by hand and which this makes structural.
func (i Intervention) Delivered() bool { return i.Decision == Applied }

// Proven reports whether the claim was checked against a baseline and
// the check concluded anything.
//
// A claim on its own is never proof, however carefully it was computed.
func (i Intervention) Proven() bool {
	return i.Delivered() && i.Verdict.Conclusive()
}

// Result is what a comparison concluded.
type Result string

const (
	// Inconclusive — not enough evidence to say. The zero Result, and
	// deliberately distinct from NoEffect: "nobody measured this" and
	// "this was measured and does nothing" lead to opposite decisions.
	Inconclusive Result = ""
	// Improved — the intervention helped, on consumption, without
	// costing outcomes.
	Improved Result = "improved"
	// NoEffect — measured, and the difference does not matter.
	NoEffect Result = "no_effect"
	// Harmed — the intervention made things worse, on consumption or on
	// outcomes.
	Harmed Result = "harmed"
)

// Verdict is what comparing an intervention against a baseline
// concluded.
type Verdict struct {
	Outcome Result `json:"outcome,omitempty"`
	// Observed is the measured difference: baseline minus intervention,
	// so a positive figure is a saving. It carries its own provenance,
	// because a verdict built on an estimate is worth what the estimate
	// is worth.
	Observed measurement.Value `json:"observed,omitzero"`
	// Samples is how many executions the comparison ran over. A verdict
	// from one run is not a verdict.
	Samples int `json:"samples"`
	// Caveat explains a harm, or why nothing could be concluded.
	Caveat string    `json:"caveat,omitempty"`
	At     time.Time `json:"at,omitzero"`
}

// Conclusive reports whether the comparison concluded anything.
func (v Verdict) Conclusive() bool { return v.Outcome != Inconclusive }

// Helped reports whether the intervention is worth keeping.
func (v Verdict) Helped() bool { return v.Outcome == Improved }

// Outcomes counts how a set of executions ended.
//
// It is what stops this package from optimizing consumption alone. A
// change that halves tokens and halves the success rate is not a saving,
// and without outcome counts there is nothing to notice that with.
type Outcomes struct {
	Achieved    int `json:"achieved"`
	Partial     int `json:"partial"`
	NotAchieved int `json:"not_achieved"`
	// Unknown counts executions nobody assessed. They are excluded from
	// the success rate rather than counted as failures: an unassessed
	// run is not a failed run, and treating it as one would make every
	// comparison look like harm.
	Unknown int `json:"unknown"`
}

// assessed is how many executions anything actually judged.
func (o Outcomes) assessed() int { return o.Achieved + o.Partial + o.NotAchieved }

// SuccessRate is the share of assessed executions that met their goal,
// counting a partial as half.
//
// The bool keeps an unassessed set from reading as a rate of zero, which
// would make "we measured nothing" look like total failure.
func (o Outcomes) SuccessRate() (float64, bool) {
	n := o.assessed()
	if n == 0 {
		return 0, false
	}
	return (float64(o.Achieved) + 0.5*float64(o.Partial)) / float64(n), true
}

// Assignment says how executions ended up in one cohort or the other.
//
// It is the difference between a comparison and an experiment, and it
// decides what a verdict is allowed to claim.
type Assignment string

const (
	// Observational — the cohorts were found, not made. Executions are
	// split by whether an optimization happened to fire, which means the
	// groups differ in ways that have nothing to do with the
	// intervention: compression applies to large outputs, routing
	// applies to turns a classifier thought were mechanical. The zero
	// value, so a caller who does not say gets the weaker reading.
	Observational Assignment = ""
	// Randomised — TokenOps decided which executions got the
	// intervention, so the cohorts differ only by that decision.
	Randomised Assignment = "randomised"
)

// Comparison is a baseline and an intervention measured over real
// executions.
type Comparison struct {
	// Assignment says how the cohorts were formed. Unstated means
	// observational.
	Assignment Assignment
	// Baseline is what the work consumed without the intervention.
	Baseline measurement.Value
	// Intervention is what it consumed with it.
	Intervention measurement.Value
	// Samples is how many executions each side covers.
	Samples int
	// BaselineOutcomes and InterventionOutcomes are how those
	// executions ended.
	BaselineOutcomes     Outcomes
	InterventionOutcomes Outcomes
	At                   time.Time
}

// minSamples is the fewest executions a verdict may rest on.
//
// The number is a judgement, not a statistical test: it exists to stop
// one lucky run being reported as a win. A real power calculation
// belongs here once there is enough data to calibrate it against.
const minSamples = 5

// materialDelta is the fraction of the baseline a difference must reach
// before it is worth calling a difference. Below it, an operator acting
// on the number would be acting on noise.
const materialDelta = 0.02

// outcomeTolerance is how far the success rate may fall before a
// consumption win is judged harm. Small on purpose: work that fails is
// work that gets done again, so a drop in success is usually more
// expensive than the tokens it saved.
const outcomeTolerance = 0.05

// Judge compares a baseline against an intervention and says what it
// concludes.
//
// The order of the checks is the argument. Evidence first — a verdict
// nothing can support is inconclusive, not neutral. Then outcomes, so a
// consumption win that cost success is named as harm rather than
// reported as a saving. Only then the consumption difference.
func Judge(c Comparison) Verdict {
	v := Verdict{Samples: c.Samples, At: c.At}

	if c.Samples < minSamples {
		v.Caveat = fmt.Sprintf(
			"%d execution(s) is not enough to conclude anything; %d are needed",
			c.Samples, minSamples)
		return v
	}

	base, baseKnown := c.Baseline.Amount()
	with, withKnown := c.Intervention.Amount()
	if !baseKnown || !withKnown {
		v.Caveat = "the baseline or the intervention was never measured, " +
			"so there is nothing to compare"
		return v
	}

	// Outcomes before consumption. An intervention that made the work
	// fail more often is harmful however many tokens it removed, and
	// judging on consumption first would report it as a win.
	baseRate, baseRated := c.BaselineOutcomes.SuccessRate()
	withRate, withRated := c.InterventionOutcomes.SuccessRate()
	if baseRated && withRated && withRate < baseRate-outcomeTolerance {
		v.Outcome = Harmed
		v.Observed = measurement.Measured(base-with, c.Intervention.Source())
		v.Caveat = fmt.Sprintf(
			"success rate fell from %.0f%% to %.0f%%; work that fails gets done again, "+
				"which costs more than the consumption this saved",
			baseRate*100, withRate*100)
		// Harm is reported from observational data too, with the
		// confound disclosed. Causation is unproven either way, but the
		// cost of pausing an innocent optimization is far below the cost
		// of continuing a guilty one.
		if c.Assignment != Randomised {
			v.Caveat += "; " + observationalCaveat
		}
		return v
	}

	delta := base - with
	v.Observed = measurement.Measured(delta, c.Intervention.Source())

	// Everything below this line is a causal claim, and an observational
	// split cannot support one. The cohorts were found rather than made,
	// so they differ in ways that have nothing to do with the
	// intervention, and attributing the difference to it would be
	// "tokens removed × nominal price" wearing a statistical costume.
	//
	// The measured difference is still reported: it is the reason to run
	// a real experiment, and withholding it would be its own dishonesty.
	if c.Assignment != Randomised {
		v.Caveat = observationalCaveat
		return v
	}

	if base != 0 && math.Abs(delta)/math.Abs(base) < materialDelta {
		v.Outcome = NoEffect
		return v
	}
	if delta > 0 {
		v.Outcome = Improved
		return v
	}
	v.Outcome = Harmed
	v.Caveat = "the intervention consumed more than the baseline"
	return v
}

// observationalCaveat explains why a difference is not a finding.
const observationalCaveat = "the cohorts were not assigned — executions were split by " +
	"whether the intervention happened to fire, so they differ in ways unrelated to it. " +
	"The difference is real; attributing it to the intervention is not yet warranted"
