// Package verify attributes recorded events to the attempts they were
// part of, and compares attempts that got an intervention against those
// that did not.
//
// It closes the last join in the loop. Events carried an actor and a
// timestamp; executions carried an actor and a span; nothing put the two
// together, so consumption could never be attributed to an attempt at a
// goal — and a comparison with nothing to compare is not a comparison.
//
// # What this deliberately does not do
//
// It does not conclude that an optimization worked.
//
// The only cohort signal available today is whether an optimization
// happened to fire, and that is observational by construction.
// Compression applies to large outputs. Routing applies to turns a
// classifier thought were mechanical. The two groups therefore differ in
// ways that have nothing to do with the intervention, and a difference
// between them cannot be attributed to it — doing so would be "tokens
// removed × nominal price" wearing a statistical costume, which is the
// claim ADR 0004 exists to refuse.
//
// So the comparison is built, the difference is reported, and the
// verdict says it is not yet warranted. That difference is the reason to
// run a real experiment; the experiment is what TokenOps has to earn
// before it calls any saving proven.
package verify

import (
	"fmt"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/work"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Attributed is one execution with the consumption recorded during it.
type Attributed struct {
	Execution work.Execution `json:"execution"`
	// Tokens is what the attempt consumed, with provenance. Unknown
	// rather than zero when no event joined to it: a missing join and a
	// genuinely free attempt are different facts, and zero is the one
	// that quietly reads as good news.
	Tokens measurement.Value `json:"tokens"`
	// Latency is mean proxy-observed request latency during the execution.
	// No matching request is unknown rather than zero.
	Latency measurement.Value `json:"latency"`
	// Events is how many events were attributed.
	Events int `json:"events"`
	// Intervened reports whether an optimization was applied during this
	// attempt. It is the cohort signal, and it is observational.
	Intervened bool `json:"intervened"`
	// Outcomes contains the strongest explicit assessment linked directly
	// to this execution. Unassessed attempts count as unknown, not failure.
	Outcomes intervention.Outcomes `json:"outcomes"`
}

// OutcomeSummary is the adapter-safe cohort result for presentation.
type OutcomeSummary struct {
	Achieved    int               `json:"achieved"`
	Partial     int               `json:"partial"`
	NotAchieved int               `json:"not_achieved"`
	Unknown     int               `json:"unknown"`
	SuccessRate measurement.Value `json:"success_rate,omitzero"`
}

// Attribute joins events to the executions they fall inside.
//
// An event belongs to an execution when the actor matches and the
// timestamp falls within the span. The actor check is not optional: two
// agents working concurrently is the normal case, and without it one
// session's consumption would be charged to another's goal.
func Attribute(execs []work.Execution, events []*eventschema.Envelope) []Attributed {
	if len(execs) == 0 {
		return nil
	}
	out := make([]Attributed, 0, len(execs))
	for _, e := range execs {
		out = append(out, attributeOne(e, events))
	}
	return out
}

func attributeOne(e work.Execution, events []*eventschema.Envelope) Attributed {
	a := Attributed{Execution: e, Outcomes: intervention.Outcomes{Unknown: 1}}

	var tokens int64
	var latency time.Duration
	var latencyEvents int64
	var countedPrompts int64
	var promptEvents int64
	var outcomeEvents []*eventschema.Envelope
	for _, env := range events {
		if env == nil {
			continue
		}
		if _, ok := env.Payload.(*eventschema.OutcomeEvent); ok && env.Association.Execution == string(e.ID) {
			outcomeEvents = append(outcomeEvents, env)
			a.Events++
			continue
		}
		if !within(e, env) {
			continue
		}
		a.Events++
		switch p := env.Payload.(type) {
		case *eventschema.PromptEvent:
			promptEvents++
			if p.Latency > 0 {
				latency += p.Latency
				latencyEvents++
			}
			// An uncounted event contributes no tokens and should not
			// make the total look smaller than it was. Phase 1 put that
			// flag on the event for exactly this kind of consumer.
			if p.TokensCounted() {
				tokens += p.TotalTokens
				countedPrompts++
			}
		case *eventschema.OptimizationEvent:
			if p.Decision == eventschema.OptimizationDecisionApplied {
				a.Intervened = true
			}
		}
	}
	if len(outcomeEvents) > 0 {
		a.Outcomes = outcomeCount(outcomes.Resolve(outcomeEvents))
	}

	if promptEvents == 0 || countedPrompts == 0 {
		a.Tokens = measurement.Unknown(
			"no events with counted prompt usage joined to this attempt — consumption is unknown")
	} else {
		a.Tokens = measurement.Measured(float64(tokens), "sqlite_events").
			At(e.StartedAt).
			Covering(countedPrompts, promptEvents-countedPrompts)
	}
	if latencyEvents == 0 {
		a.Latency = measurement.Unknown("no prompt events with measured latency joined to this attempt")
	} else {
		a.Latency = measurement.Measured(float64(latency)/float64(latencyEvents)/float64(time.Millisecond), "sqlite_events").
			At(e.StartedAt).
			Covering(latencyEvents, promptEvents-latencyEvents)
	}
	return a
}

func outcomeCount(out eventschema.OutcomeEvent) intervention.Outcomes {
	counts := intervention.Outcomes{}
	switch out.Result {
	case eventschema.OutcomeAchieved:
		counts.Achieved = 1
	case eventschema.OutcomePartial:
		counts.Partial = 1
	case eventschema.OutcomeNotAchieved:
		counts.NotAchieved = 1
	default:
		counts.Unknown = 1
	}
	return counts
}

// within reports whether an event belongs to an execution.
//
// A running execution has no end, so everything after its start counts:
// the attempt is still accumulating.
func within(e work.Execution, env *eventschema.Envelope) bool {
	if string(e.By) == "" || env.Association.Actor != string(e.By) {
		return false
	}
	at := env.Timestamp
	if at.Before(e.StartedAt) {
		return false
	}
	if e.Running() {
		return true
	}
	return !at.After(e.EndedAt)
}

// Report is what a comparison over real executions found, and what it is
// allowed to claim.
type Report struct {
	Comparison intervention.Comparison `json:"comparison"`
	Verdict    intervention.Verdict    `json:"verdict"`
	// BaselineCount and InterventionCount say how many attempts fell in
	// each cohort. "Not enough data" without saying which side was short
	// is advice nobody can act on.
	BaselineCount        int               `json:"baseline_count"`
	InterventionCount    int               `json:"intervention_count"`
	BaselineOutcomes     OutcomeSummary    `json:"baseline_outcomes"`
	InterventionOutcomes OutcomeSummary    `json:"intervention_outcomes"`
	BaselineLatency      measurement.Value `json:"baseline_latency_ms"`
	InterventionLatency  measurement.Value `json:"intervention_latency_ms"`
	// Attributed is the per-execution detail the comparison rests on.
	Attributed []Attributed `json:"attributed,omitempty"`
}

// Compare splits attributed executions by whether an intervention fired
// and judges the difference.
//
// The split is observational and the Comparison says so, which is what
// stops the verdict claiming the intervention helped. See the package
// comment: this is a deliberate refusal, not a missing feature.
func Compare(execs []work.Execution, events []*eventschema.Envelope) Report {
	attributed := Attribute(execs, events)

	var baseline, intervened []Attributed
	for _, a := range attributed {
		if a.Intervened {
			intervened = append(intervened, a)
			continue
		}
		baseline = append(baseline, a)
	}

	report := Report{
		Attributed:        attributed,
		BaselineCount:     len(baseline),
		InterventionCount: len(intervened),
	}

	report.Comparison = intervention.Comparison{
		Assignment:   intervention.Observational,
		Baseline:     meanTokens(baseline),
		Intervention: meanTokens(intervened),
		// The comparison is only as strong as its smaller side: ten
		// baselines against one intervention is one observation, not ten.
		Samples:              min(len(baseline), len(intervened)),
		BaselineOutcomes:     sumOutcomes(baseline),
		InterventionOutcomes: sumOutcomes(intervened),
	}
	report.BaselineOutcomes = summarizeOutcomes(report.Comparison.BaselineOutcomes)
	report.InterventionOutcomes = summarizeOutcomes(report.Comparison.InterventionOutcomes)
	report.BaselineLatency = meanLatency(baseline)
	report.InterventionLatency = meanLatency(intervened)
	report.Verdict = intervention.Judge(report.Comparison)

	// Replace the domain's sample-size caveat when it is the wrong
	// diagnosis. "0 executions is not enough; 5 are needed" sends an
	// operator to wait for more data of a kind their pipeline is
	// producing none of — the same class of unhelpful answer as
	// reporting a refused poller as an unused vendor.
	if !report.Verdict.Conclusive() {
		switch {
		case len(attributed) == 0:
			report.Verdict.Caveat = "no attempts were reconstructed in this window — " +
				"there are no transcripts to group, or none inside it"
		case report.Comparison.Samples < 1:
			report.Verdict.Caveat = fmt.Sprintf(
				"%d attempt(s) without an intervention and %d with; "+
					"a comparison needs some of each",
				len(baseline), len(intervened))
		}
	}
	return report
}

func meanLatency(as []Attributed) measurement.Value {
	if len(as) == 0 {
		return measurement.Unknown("the cohort is empty")
	}
	values := make([]measurement.Value, 0, len(as))
	for _, a := range as {
		values = append(values, a.Latency)
	}
	total := measurement.Sum(values...)
	amount, ok := total.Amount()
	if !ok {
		return total
	}
	return measurement.Measured(amount/float64(len(as)), total.Source()).
		WithCaveat(total.Caveat())
}

func sumOutcomes(as []Attributed) intervention.Outcomes {
	var total intervention.Outcomes
	for _, a := range as {
		total.Achieved += a.Outcomes.Achieved
		total.Partial += a.Outcomes.Partial
		total.NotAchieved += a.Outcomes.NotAchieved
		total.Unknown += a.Outcomes.Unknown
	}
	return total
}

func summarizeOutcomes(o intervention.Outcomes) OutcomeSummary {
	s := OutcomeSummary{Achieved: o.Achieved, Partial: o.Partial, NotAchieved: o.NotAchieved, Unknown: o.Unknown}
	if rate, ok := o.SuccessRate(); ok {
		assessed := o.Achieved + o.Partial + o.NotAchieved
		s.SuccessRate = measurement.Measured(rate*100, "outcome_events").Covering(int64(assessed), int64(o.Unknown))
	} else {
		s.SuccessRate = measurement.Unknown("no outcomes were assessed")
	}
	return s
}

// Reading is the one-line judgement, in the words a surface prints.
//
// It lives here rather than in each adapter so the CLI and the MCP tool
// cannot describe the same verdict differently — and so neither has to
// import the intervention domain to dispatch on a Result, which is the
// direct adapter → domain import the architecture ratchet refuses.
func (r Report) Reading() string {
	switch r.Verdict.Outcome {
	case intervention.Harmed:
		return "HARM — the attempts an optimization touched did worse"
	case intervention.Improved:
		return "improvement"
	case intervention.NoEffect:
		return "no meaningful difference"
	default:
		return "nothing can be concluded yet"
	}
}

// Harmful reports the one reading worth acting on immediately.
//
// Causation is unproven either way, but the cost of pausing an innocent
// optimization is far below the cost of continuing a guilty one.
func (r Report) Harmful() bool { return r.Verdict.Outcome == intervention.Harmed }

// Observational reports whether the cohorts were found rather than made,
// which is what stops any of this being called proof.
func (r Report) Observational() bool {
	return r.Comparison.Assignment != intervention.Randomised
}

// meanTokens averages a cohort's consumption.
//
// Sum carries the weakest quality of its inputs, so a cohort containing
// one unjoined execution yields an unknown mean rather than an average
// that quietly excludes it.
func meanTokens(as []Attributed) measurement.Value {
	if len(as) == 0 {
		return measurement.Unknown("the cohort is empty")
	}
	values := make([]measurement.Value, 0, len(as))
	for _, a := range as {
		values = append(values, a.Tokens)
	}
	total := measurement.Sum(values...)
	amount, ok := total.Amount()
	if !ok {
		return total
	}
	return measurement.Measured(amount/float64(len(as)), total.Source()).
		WithCaveat(total.Caveat())
}

// CompareReconstructed is Compare over the output of the reconstruct
// capability.
//
// It exists so an adapter never has to name work.Execution to use this:
// reaching into the ontology from internal/cli is the direct
// adapter → domain import the architecture ratchet refuses, and it
// refuses it because a capability assembled inside one adapter is one
// the other assembles differently.
func CompareReconstructed(rs []reconstruct.Work, events []*eventschema.Envelope) Report {
	execs := make([]work.Execution, 0, len(rs))
	for _, r := range rs {
		execs = append(execs, r.Execution)
	}
	return Compare(execs, events)
}

// SortByStart orders attributed executions oldest first, for a caller
// rendering a timeline.
func SortByStart(as []Attributed) {
	sort.Slice(as, func(i, j int) bool {
		return as[i].Execution.StartedAt.Before(as[j].Execution.StartedAt)
	})
}
