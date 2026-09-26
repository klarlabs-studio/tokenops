// Package verify attributes recorded events to the attempts they were
// part of, compares observational cohorts, and uses complete execution-
// linked experiment pairs when assignment and outcome evidence permits.
//
// It closes the last join in the loop. Events carried an actor and a
// timestamp; executions carried an actor and a span; nothing put the two
// together, so consumption could never be attributed to an attempt at a
// goal — and a comparison with nothing to compare is not a comparison.
//
// # Evidence boundary
//
// An applied-versus-not-applied split is observational by construction.
// Compression applies to large outputs. Routing applies to turns a
// classifier thought were mechanical. The two groups therefore differ in
// ways unrelated to the intervention. A randomized comparison is allowed
// only for complete linked pairs with measured tokens and explicit
// outcomes; missing or mixed evidence falls back to the observational view.
package verify

import (
	"fmt"
	"math"
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
	// Experiment fields come only from execution-associated assignment
	// events; they are absent for ordinary or ambiguous observations.
	ExperimentID   string `json:"experiment_id,omitempty"`
	ExperimentPair int    `json:"experiment_pair,omitempty"`
	ExperimentArm  string `json:"experiment_arm,omitempty"`
	// Tokens is what the attempt consumed, with provenance. Unknown
	// rather than zero when no event joined to it: a missing join and a
	// genuinely free attempt are different facts, and zero is the one
	// that quietly reads as good news.
	Tokens measurement.Value `json:"tokens"`
	// MeteredCostUSD includes only events explicitly priced with an
	// event-time rate card. Subscription quota and trials stay separate.
	MeteredCostUSD measurement.Value `json:"metered_cost_usd"`
	// HumanAttentionMinutes is optional operator self-report, never inferred
	// from transcript length or agent-generated estimates.
	HumanAttentionMinutes measurement.Value `json:"human_attention_minutes"`
	// Latency is mean proxy-observed request latency during the execution.
	// No matching request is unknown rather than zero.
	Latency measurement.Value `json:"latency"`
	// PlanQuotaTokens keeps flat-rate quota usage separate from metered
	// consumption; provider is the resource key, not a dollar conversion.
	PlanQuotaTokens map[string]int64 `json:"plan_quota_tokens,omitempty"`
	// Events is how many events were attributed.
	Events int `json:"events"`
	// Intervened reports whether an optimization was applied during this
	// attempt. It is the cohort signal, and it is observational.
	Intervened bool `json:"intervened"`
	// InterventionKinds contains canonical applied decision kinds. When an
	// applied decision also has a legacy optimization event, that paired
	// event is not double-counted as a second kind.
	InterventionKinds []string `json:"intervention_kinds,omitempty"`
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

// QuotaSummary reports measured plan-included tokens by provider. These
// are quota consumption, not monetary cost, and are never combined across
// providers into one synthetic unit.
type QuotaSummary map[string]measurement.Value

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
	a := Attributed{Execution: e, Outcomes: intervention.Outcomes{Unknown: 1}, PlanQuotaTokens: make(map[string]int64)}

	var tokens int64
	var meteredCost float64
	var meteredCostEvents int64
	var latency time.Duration
	var latencyEvents int64
	var countedPrompts int64
	var promptEvents int64
	var outcomeEvents []*eventschema.Envelope
	var appliedDecisions = make(map[string]string)
	var appliedOptimizations []*eventschema.Envelope
	for _, env := range events {
		if env == nil {
			continue
		}
		if env.Association.Execution == string(e.ID) {
			if p, ok := env.Payload.(*eventschema.ExperimentEvent); ok && p.Stage == eventschema.ExperimentAssigned {
				a.ExperimentID = env.Correlation.Experiment
				a.ExperimentPair = p.Pair
				a.ExperimentArm = p.Assignment
				a.Events++
				continue
			}
		}
		if _, ok := env.Payload.(*eventschema.OutcomeEvent); ok &&
			env.Association.Execution == string(e.ID) && !env.Timestamp.Before(e.StartedAt) {
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
			if (p.CostSource == "" || p.CostSource == eventschema.CostSourceMetered) && p.CostMeasured {
				meteredCost += p.CostUSD
				meteredCostEvents++
			}
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
				if p.CostSource == eventschema.CostSourcePlanIncluded {
					a.PlanQuotaTokens[string(p.Provider)] += p.TotalTokens
				}
			}
		case *eventschema.OptimizationEvent:
			if p.Decision == eventschema.OptimizationDecisionApplied {
				a.Intervened = true
				appliedOptimizations = append(appliedOptimizations, env)
			}
		case *eventschema.DecisionEvent:
			if p.Stage == eventschema.DecisionStageApplied {
				a.Intervened = true
				appliedDecisions[env.Correlation.Decision] = p.Kind
			}
		}
	}
	kinds := make(map[string]struct{}, len(appliedDecisions)+len(appliedOptimizations))
	for _, kind := range appliedDecisions {
		if kind != "" {
			kinds[kind] = struct{}{}
		}
	}
	for _, env := range appliedOptimizations {
		if env.Correlation.Decision != "" {
			if _, paired := appliedDecisions[env.Correlation.Decision]; paired {
				continue
			}
		}
		if p, ok := env.Payload.(*eventschema.OptimizationEvent); ok && p.Kind != "" {
			kinds[string(p.Kind)] = struct{}{}
		}
	}
	for kind := range kinds {
		a.InterventionKinds = append(a.InterventionKinds, kind)
	}
	sort.Strings(a.InterventionKinds)
	if len(outcomeEvents) > 0 {
		a.Outcomes = outcomeCount(outcomes.Resolve(outcomeEvents))
	}
	a.HumanAttentionMinutes = attentionMinutes(outcomeEvents)

	if promptEvents == 0 || countedPrompts == 0 {
		a.Tokens = measurement.Unknown(
			"no events with counted prompt usage joined to this attempt — consumption is unknown")
	} else {
		a.Tokens = measurement.Measured(float64(tokens), "sqlite_events").
			At(e.StartedAt).
			Covering(countedPrompts, promptEvents-countedPrompts)
	}
	if meteredCostEvents == 0 {
		a.MeteredCostUSD = measurement.Unknown("no prompt events with verified metered pricing joined to this attempt")
	} else {
		a.MeteredCostUSD = measurement.Measured(meteredCost, "sqlite_events").
			At(e.StartedAt).
			Covering(meteredCostEvents, promptEvents-meteredCostEvents)
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
	if env.Association.Execution != "" {
		if env.Association.Execution != string(e.ID) {
			return false
		}
	} else if string(e.By) == "" || env.Association.Actor != string(e.By) {
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
	BaselineCount                     int               `json:"baseline_count"`
	InterventionCount                 int               `json:"intervention_count"`
	BaselineOutcomes                  OutcomeSummary    `json:"baseline_outcomes"`
	InterventionOutcomes              OutcomeSummary    `json:"intervention_outcomes"`
	BaselineLatency                   measurement.Value `json:"baseline_latency_ms"`
	InterventionLatency               measurement.Value `json:"intervention_latency_ms"`
	BaselineMeteredCostUSD            measurement.Value `json:"baseline_metered_cost_usd"`
	InterventionMeteredCostUSD        measurement.Value `json:"intervention_metered_cost_usd"`
	BaselineHumanAttentionMinutes     measurement.Value `json:"baseline_human_attention_minutes"`
	InterventionHumanAttentionMinutes measurement.Value `json:"intervention_human_attention_minutes"`
	BaselinePlanQuota                 QuotaSummary      `json:"baseline_plan_quota_tokens"`
	InterventionPlanQuota             QuotaSummary      `json:"intervention_plan_quota_tokens"`
	RandomizedExperimentID            string            `json:"randomized_experiment_id,omitempty"`
	RandomizedPairs                   int               `json:"randomized_pairs,omitempty"`
	RandomizedFallbackReason          string            `json:"randomized_fallback_reason,omitempty"`
	// Attributed is the per-execution detail the comparison rests on.
	Attributed []Attributed `json:"attributed,omitempty"`
}

// Compare prefers a complete execution-linked randomized experiment when
// exactly one is present; otherwise it compares applied-versus-not-applied
// observational cohorts.
func Compare(execs []work.Execution, events []*eventschema.Envelope) Report {
	return CompareExperiment(execs, events, "")
}

// CompareExperiment prefers an execution-linked randomized comparison for
// experimentID. With an empty ID it auto-selects only when a single
// experiment is represented; otherwise it preserves the observational view.
func CompareExperiment(execs []work.Execution, events []*eventschema.Envelope, experimentID string) Report {
	attributed := Attribute(execs, events)

	var baseline, intervened []Attributed
	for _, a := range attributed {
		if a.Intervened {
			intervened = append(intervened, a)
			continue
		}
		baseline = append(baseline, a)
	}

	assignment := intervention.Observational
	randomizedID, randomBaseline, randomVariant, randomizedPairs, fallback := randomizedCohorts(attributed, events, experimentID)
	if randomizedID != "" {
		baseline, intervened = randomBaseline, randomVariant
		assignment = intervention.Randomised
		compared := make(map[string]struct{}, len(baseline)+len(intervened))
		for _, a := range baseline {
			compared[string(a.Execution.ID)] = struct{}{}
		}
		for _, a := range intervened {
			compared[string(a.Execution.ID)] = struct{}{}
		}
		selected := make([]Attributed, 0, len(compared))
		for _, a := range attributed {
			if _, ok := compared[string(a.Execution.ID)]; ok {
				selected = append(selected, a)
			}
		}
		attributed = selected
	}
	report := Report{
		Attributed:               attributed,
		BaselineCount:            len(baseline),
		InterventionCount:        len(intervened),
		RandomizedExperimentID:   randomizedID,
		RandomizedPairs:          randomizedPairs,
		RandomizedFallbackReason: fallback,
	}

	report.Comparison = intervention.Comparison{
		Assignment:   assignment,
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
	report.BaselineMeteredCostUSD = meanMeteredCost(baseline)
	report.InterventionMeteredCostUSD = meanMeteredCost(intervened)
	report.BaselineHumanAttentionMinutes = meanAttention(baseline)
	report.InterventionHumanAttentionMinutes = meanAttention(intervened)
	report.BaselinePlanQuota = sumPlanQuota(baseline)
	report.InterventionPlanQuota = sumPlanQuota(intervened)
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

func attentionMinutes(events []*eventschema.Envelope) measurement.Value {
	var latest time.Time
	var amount float64
	found := false
	for _, env := range events {
		if env == nil {
			continue
		}
		out, ok := env.Payload.(*eventschema.OutcomeEvent)
		if !ok || out.Assessment != eventschema.OutcomeHuman {
			continue
		}
		for _, metric := range out.Metrics {
			if metric.Name != "human_attention_minutes" || metric.Unit != "minutes" || metric.Source != "human_self_report" || metric.Value < 0 || math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
				continue
			}
			observed := metric.ObservedAt
			if observed.IsZero() {
				observed = env.Timestamp
			}
			if !found || observed.After(latest) {
				latest, amount, found = observed, metric.Value, true
			}
		}
	}
	if !found {
		return measurement.Unknown("no operator-reported human attention time for this execution")
	}
	return measurement.Measured(amount, "human_outcome_events").At(latest)
}

func meanAttention(as []Attributed) measurement.Value {
	if len(as) == 0 {
		return measurement.Unknown("the cohort is empty")
	}
	values := make([]measurement.Value, 0, len(as))
	for _, a := range as {
		values = append(values, a.HumanAttentionMinutes)
	}
	total := measurement.Sum(values...)
	amount, ok := total.Amount()
	if !ok {
		return total
	}
	return measurement.Measured(amount/float64(len(as)), total.Source()).WithCaveat(total.Caveat())
}

// randomizedCohorts accepts only complete randomized pairs whose every
// assigned execution was reconstructed and has measured tokens plus an
// explicit outcome. Missing assignments or outcomes can bias an estimate,
// so any such gap falls back to the full observational comparison.
func randomizedCohorts(as []Attributed, events []*eventschema.Envelope, requested string) (string, []Attributed, []Attributed, int, string) {
	type pairArms map[string]string
	assignments := make(map[string]map[int]pairArms)
	seenIDs := make(map[string]struct{})
	seenExecutions := make(map[string]string)
	for _, env := range events {
		if env == nil || env.Correlation.Experiment == "" {
			continue
		}
		p, ok := env.Payload.(*eventschema.ExperimentEvent)
		if !ok || p.Stage != eventschema.ExperimentAssigned {
			continue
		}
		id := env.Correlation.Experiment
		seenIDs[id] = struct{}{}
		if requested != "" && id != requested {
			continue
		}
		if env.Association.Execution == "" {
			return "", nil, nil, 0, "the experiment has unlinked assignments; using observational cohorts"
		}
		executionID := env.Association.Execution
		assignmentKey := fmt.Sprintf("%s:%d:%s", id, p.Pair, p.Assignment)
		if _, duplicate := seenExecutions[executionID]; duplicate {
			return "", nil, nil, 0, "an execution has multiple randomized assignments; using observational cohorts"
		}
		seenExecutions[executionID] = assignmentKey
		if p.Pair < 1 || (p.Assignment != "baseline" && p.Assignment != "variant") {
			return "", nil, nil, 0, "experiment assignment evidence is malformed; using observational cohorts"
		}
		pairs := assignments[id]
		if pairs == nil {
			pairs = make(map[int]pairArms)
			assignments[id] = pairs
		}
		arms := pairs[p.Pair]
		if arms == nil {
			arms = make(pairArms)
			pairs[p.Pair] = arms
		}
		if _, duplicate := arms[p.Assignment]; duplicate {
			return "", nil, nil, 0, "an experiment arm has duplicate execution assignments; using observational cohorts"
		}
		arms[p.Assignment] = env.Association.Execution
	}
	if requested == "" && len(seenIDs) > 1 {
		return "", nil, nil, 0, "multiple experiments are present; select one with --experiment-id to avoid mixing trials"
	}
	id := requested
	if id == "" {
		for candidate := range assignments {
			id = candidate
		}
	}
	if id == "" || len(assignments[id]) == 0 {
		if requested != "" {
			return "", nil, nil, 0, "the selected experiment has no execution-linked assignments in this window; using observational cohorts"
		}
		return "", nil, nil, 0, ""
	}
	for pair := 1; pair <= len(assignments[id]); pair++ {
		if _, ok := assignments[id][pair]; !ok {
			return "", nil, nil, 0, "the selected experiment has a gap in its pair ledger; using observational cohorts"
		}
	}
	byExecution := make(map[string]Attributed, len(as))
	for _, a := range as {
		byExecution[string(a.Execution.ID)] = a
	}
	var baseline, variant []Attributed
	for _, arms := range assignments[id] {
		baselineID, hasBaseline := arms["baseline"]
		variantID, hasVariant := arms["variant"]
		if !hasBaseline || !hasVariant || len(arms) != 2 {
			return "", nil, nil, 0, "the selected experiment contains an incomplete randomized pair; using observational cohorts"
		}
		base, baseOK := byExecution[baselineID]
		with, variantOK := byExecution[variantID]
		if !baseOK || !variantOK {
			return "", nil, nil, 0, "an assigned execution was not reconstructed; using observational cohorts"
		}
		if !base.Tokens.Known() || !with.Tokens.Known() || base.Outcomes.Unknown > 0 || with.Outcomes.Unknown > 0 {
			return "", nil, nil, 0, "randomized executions need measured tokens and explicit outcomes; using observational cohorts"
		}
		baseline = append(baseline, base)
		variant = append(variant, with)
	}
	return id, baseline, variant, len(assignments[id]), ""
}

func meanMeteredCost(as []Attributed) measurement.Value {
	if len(as) == 0 {
		return measurement.Unknown("the cohort is empty")
	}
	values := make([]measurement.Value, 0, len(as))
	for _, a := range as {
		values = append(values, a.MeteredCostUSD)
	}
	total := measurement.Sum(values...)
	amount, ok := total.Amount()
	if !ok {
		return total
	}
	return measurement.Measured(amount/float64(len(as)), total.Source()).WithCaveat(total.Caveat())
}

func sumPlanQuota(as []Attributed) QuotaSummary {
	totals := make(map[string]int64)
	counts := make(map[string]int64)
	for _, a := range as {
		for provider, tokens := range a.PlanQuotaTokens {
			totals[provider] += tokens
			counts[provider]++
		}
	}
	if len(totals) == 0 {
		return nil
	}
	quota := make(QuotaSummary, len(totals))
	for provider, tokens := range totals {
		quota[provider] = measurement.Measured(float64(tokens), "sqlite_events").
			Covering(counts[provider], int64(len(as))-counts[provider])
	}
	return quota
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
	return CompareReconstructedExperiment(rs, events, "")
}

// CompareReconstructedExperiment compares reconstructed work and optionally
// selects one explicitly named randomized trial.
func CompareReconstructedExperiment(rs []reconstruct.Work, events []*eventschema.Envelope, experimentID string) Report {
	execs := make([]work.Execution, 0, len(rs))
	for _, r := range rs {
		execs = append(execs, r.Execution)
	}
	// A proxy bridge records an explicit execution ID on each event, but it
	// cannot invent the user's work goal or guarantee a transcript exists.
	// For a selected randomized trial, those durable IDs are sufficient to
	// compare resource use and explicitly recorded outcomes without claiming
	// that TokenOps reconstructed the work itself.
	execs = appendMissingAssignedExecutions(execs, events, experimentID)
	return CompareExperiment(execs, events, experimentID)
}

func appendMissingAssignedExecutions(execs []work.Execution, events []*eventschema.Envelope, requested string) []work.Execution {
	known := make(map[string]struct{}, len(execs))
	for _, exec := range execs {
		known[string(exec.ID)] = struct{}{}
	}
	seenExperiments := make(map[string]struct{})
	starts := make(map[string]time.Time)
	for _, env := range events {
		if env == nil || env.Association.Execution == "" || env.Correlation.Experiment == "" {
			continue
		}
		assignment, ok := env.Payload.(*eventschema.ExperimentEvent)
		if !ok || assignment.Stage != eventschema.ExperimentAssigned {
			continue
		}
		if requested != "" && env.Correlation.Experiment != requested {
			continue
		}
		seenExperiments[env.Correlation.Experiment] = struct{}{}
		id := env.Association.Execution
		if prior, ok := starts[id]; !ok || env.Timestamp.Before(prior) {
			starts[id] = env.Timestamp
		}
	}
	if requested == "" && len(seenExperiments) != 1 {
		return execs
	}
	for id, startedAt := range starts {
		if _, ok := known[id]; ok || startedAt.IsZero() {
			continue
		}
		// Work stays unknown; only the execution identity and its start are
		// evidenced by the assignment. Directly associated proxy events can
		// still be attributed by ID and timestamp.
		execs = append(execs, work.Execution{ID: work.ID(id), StartedAt: startedAt})
	}
	return execs
}

// SortByStart orders attributed executions oldest first, for a caller
// rendering a timeline.
func SortByStart(as []Attributed) {
	sort.Slice(as, func(i, j int) bool {
		return as[i].Execution.StartedAt.Before(as[j].Execution.StartedAt)
	})
}
