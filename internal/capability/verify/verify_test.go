package verify_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/work"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var t0 = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

func execution(id string, by work.ActorID, start time.Time, dur time.Duration) work.Execution {
	return work.Attempt(work.ID(id), work.ID("w-"+id), by, start).
		Ended(start.Add(dur), work.Succeeded)
}

func promptEvent(actor string, at time.Time, tokens int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: actor + at.String(), Type: eventschema.EventTypePrompt, Timestamp: at,
		Association: eventschema.Association{Actor: actor},
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-6",
			InputTokens: tokens, TotalTokens: tokens, Latency: 250 * time.Millisecond,
		},
	}
}

func TestComparisonReportsMeasuredMeanLatencyByCohort(t *testing.T) {
	execs := []work.Execution{
		execution("baseline", "session:a", t0, time.Hour),
		execution("treated", "session:a", t0.Add(2*time.Hour), time.Hour),
	}
	events := []*eventschema.Envelope{
		promptEvent("session:a", t0.Add(time.Minute), 100),
		optimizationEvent("session:a", t0.Add(2*time.Hour+time.Minute)),
		promptEvent("session:a", t0.Add(2*time.Hour+2*time.Minute), 100),
	}
	// Keep the observations intentionally different; the verifier must
	// preserve measurement units and report each cohort separately.
	(*events[2].Payload.(*eventschema.PromptEvent)).Latency = 500 * time.Millisecond
	report := verify.Compare(execs, events)
	baseline, baselineOK := report.BaselineLatency.Amount()
	treated, treatedOK := report.InterventionLatency.Amount()
	if !baselineOK || !treatedOK || baseline != 250 || treated != 500 {
		t.Fatalf("latency = %v (%v) / %v (%v) ms", baseline, baselineOK, treated, treatedOK)
	}
	if report.BaselineLatency.Quality() != measurement.QualityMeasured {
		t.Fatalf("latency provenance = %q; want measured", report.BaselineLatency.Quality())
	}
}

func TestMissingLatencyIsUnknownNotZero(t *testing.T) {
	event := promptEvent("session:a", t0.Add(time.Minute), 100)
	event.Payload.(*eventschema.PromptEvent).Latency = 0
	got := verify.Attribute(
		[]work.Execution{execution("e1", "session:a", t0, time.Hour)},
		[]*eventschema.Envelope{event},
	)
	if got[0].Latency.Known() {
		t.Fatalf("missing latency reported as measured %vms", got[0].Latency.AmountOr(-1))
	}
}

func TestPlanIncludedUsageStaysProviderScopedAndNotDollarPriced(t *testing.T) {
	base := promptEvent("session:a", t0.Add(time.Minute), 120)
	base.Payload.(*eventschema.PromptEvent).CostSource = eventschema.CostSourcePlanIncluded
	treated := promptEvent("session:a", t0.Add(2*time.Hour+time.Minute), 240)
	treated.Payload.(*eventschema.PromptEvent).CostSource = eventschema.CostSourcePlanIncluded
	execs := []work.Execution{
		execution("baseline", "session:a", t0, time.Hour),
		execution("treated", "session:a", t0.Add(2*time.Hour), time.Hour),
	}
	events := []*eventschema.Envelope{
		base,
		optimizationEvent("session:a", t0.Add(2*time.Hour+30*time.Second)),
		treated,
	}
	report := verify.Compare(execs, events)
	if got := report.BaselinePlanQuota[string(eventschema.ProviderAnthropic)].AmountOr(-1); got != 120 {
		t.Fatalf("baseline plan quota = %v tokens, want 120", got)
	}
	if got := report.InterventionPlanQuota[string(eventschema.ProviderAnthropic)].AmountOr(-1); got != 240 {
		t.Fatalf("intervention plan quota = %v tokens, want 240", got)
	}
	if report.BaselinePlanQuota[string(eventschema.ProviderAnthropic)].Source() != "sqlite_events" {
		t.Fatalf("plan quota has no event provenance: %+v", report.BaselinePlanQuota)
	}
}

func TestOnlyExplicitlyPricedMeteredCostIsAttributed(t *testing.T) {
	priced := promptEvent("session:a", t0.Add(time.Minute), 100)
	priced.Payload.(*eventschema.PromptEvent).CostUSD = 0.004
	priced.Payload.(*eventschema.PromptEvent).CostMeasured = true
	unpriced := promptEvent("session:a", t0.Add(2*time.Minute), 100)
	unpriced.Payload.(*eventschema.PromptEvent).CostUSD = 99 // stale/untrusted numeric field
	got := verify.Attribute(
		[]work.Execution{execution("e1", "session:a", t0, time.Hour)},
		[]*eventschema.Envelope{priced, unpriced},
	)[0]
	if value, ok := got.MeteredCostUSD.Amount(); !ok || value != 0.004 {
		t.Fatalf("metered cost = %v known=%v; only explicit priced event should count", value, ok)
	}
	if coverage := got.MeteredCostUSD.Coverage(); coverage.Included != 1 || coverage.Excluded != 1 {
		t.Fatalf("cost coverage = %+v; want 1 priced and 1 excluded", coverage)
	}
}

func TestUnpricedZeroIsUnknownRatherThanFree(t *testing.T) {
	event := promptEvent("session:a", t0.Add(time.Minute), 100)
	got := verify.Attribute(
		[]work.Execution{execution("e1", "session:a", t0, time.Hour)},
		[]*eventschema.Envelope{event},
	)[0]
	if got.MeteredCostUSD.Known() {
		t.Fatalf("unpriced event reported cost $%.6f", got.MeteredCostUSD.AmountOr(-1))
	}
}

func TestHumanAttentionRequiresExplicitOperatorMetric(t *testing.T) {
	exec := execution("e1", "session:a", t0, time.Hour)
	unknown := verify.Attribute([]work.Execution{exec}, nil)[0]
	if unknown.HumanAttentionMinutes.Known() {
		t.Fatalf("missing attention report inferred %.1f minutes", unknown.HumanAttentionMinutes.AmountOr(-1))
	}
	event := outcomeEvent("e1", eventschema.OutcomeAchieved, exec.EndedAt.Add(time.Minute))
	out := event.Payload.(*eventschema.OutcomeEvent)
	out.Metrics = []eventschema.OutcomeMetric{{
		Name: "human_attention_minutes", Unit: "minutes", Value: 3.5,
		Source: "human_self_report", ObservedAt: exec.EndedAt.Add(time.Minute), Confidence: 1,
	}}
	got := verify.Attribute([]work.Execution{exec}, []*eventschema.Envelope{event})[0]
	if value, ok := got.HumanAttentionMinutes.Amount(); !ok || value != 3.5 {
		t.Fatalf("attention = %v known=%v; want explicit self-report", value, ok)
	}
}

func optimizationEvent(actor string, at time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: "opt" + actor + at.String(), Type: eventschema.EventTypeOptimization, Timestamp: at,
		Association: eventschema.Association{Actor: actor},
		Payload: &eventschema.OptimizationEvent{
			Kind:     eventschema.OptimizationTypeCommandFmt,
			Decision: eventschema.OptimizationDecisionApplied,
		},
	}
}

func decisionEvent(actor, id, kind string, stage eventschema.DecisionStage, at time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: "decision-" + id, Type: eventschema.EventTypeDecision, Timestamp: at,
		Association: eventschema.Association{Actor: actor},
		Correlation: eventschema.Correlation{Decision: id, Intervention: "intervention-" + id},
		Payload:     &eventschema.DecisionEvent{Kind: kind, Stage: stage},
	}
}

func outcomeEvent(executionID string, result eventschema.OutcomeResult, at time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: "outcome-" + executionID + at.String(), Type: eventschema.EventTypeOutcome, Timestamp: at,
		Association: eventschema.Association{Execution: executionID},
		Payload:     &eventschema.OutcomeEvent{Result: result, Assessment: eventschema.OutcomeHuman},
	}
}

func experimentAssignment(executionID, experimentID, arm string, pair int, at time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: "assignment-" + executionID, Type: eventschema.EventTypeExperiment, Timestamp: at,
		Association: eventschema.Association{Execution: executionID},
		Correlation: eventschema.Correlation{Experiment: experimentID},
		Payload: &eventschema.ExperimentEvent{
			Stage: eventschema.ExperimentAssigned, Kind: "model_route", Pair: pair, Assignment: arm,
		},
	}
}

func randomizedFixture(pairCount int, experimentID, prefix string) ([]work.Execution, []*eventschema.Envelope) {
	var execs []work.Execution
	var events []*eventschema.Envelope
	for pair := 1; pair <= pairCount; pair++ {
		at := t0.Add(time.Duration(pair) * time.Hour)
		baseID, variantID := fmt.Sprintf("%s-base-%d", prefix, pair), fmt.Sprintf("%s-variant-%d", prefix, pair)
		execs = append(execs,
			execution(baseID, "session:base", at, 20*time.Minute),
			execution(variantID, "session:variant", at, 20*time.Minute),
		)
		events = append(events,
			experimentAssignment(baseID, experimentID, "baseline", pair, at),
			experimentAssignment(variantID, experimentID, "variant", pair, at),
			promptEvent("session:base", at.Add(time.Minute), 100),
			promptEvent("session:variant", at.Add(time.Minute), 80),
			outcomeEvent(baseID, eventschema.OutcomeAchieved, at.Add(2*time.Minute)),
			outcomeEvent(variantID, eventschema.OutcomeAchieved, at.Add(2*time.Minute)),
		)
	}
	return execs, events
}

func TestRandomizedComparisonUsesCompleteOutcomeLinkedPairs(t *testing.T) {
	execs, events := randomizedFixture(5, "experiment:test", "test")
	got := verify.Compare(execs, events)
	if got.Observational() || got.RandomizedExperimentID != "experiment:test" || got.RandomizedPairs != 5 {
		t.Fatalf("randomized metadata = id %q pairs %d observational %v", got.RandomizedExperimentID, got.RandomizedPairs, got.Observational())
	}
	if got.BaselineCount != 5 || got.InterventionCount != 5 || got.Verdict.Outcome != intervention.Improved {
		t.Fatalf("randomized comparison = baseline %d intervention %d verdict %+v", got.BaselineCount, got.InterventionCount, got.Verdict)
	}
	if got.Attributed[0].ExperimentID != "experiment:test" || got.Attributed[0].ExperimentArm == "" {
		t.Fatalf("assignment provenance missing from attributed execution: %+v", got.Attributed[0])
	}
}

func TestReconstructedExperimentUsesProxyExecutionEvidenceWithoutInventingWork(t *testing.T) {
	baseID, variantID := "claude:baseline", "claude:variant"
	assignedAt := t0.Add(time.Hour)
	basePrompt := promptEvent("", assignedAt.Add(time.Minute), 100)
	basePrompt.Association.Execution = baseID
	variantPrompt := promptEvent("", assignedAt.Add(time.Minute), 80)
	variantPrompt.Association.Execution = variantID
	stalePrompt := promptEvent("", t0.Add(time.Minute), 900)
	stalePrompt.Association.Execution = baseID
	staleOutcome := outcomeEvent(baseID, eventschema.OutcomeNotAchieved, t0.Add(2*time.Minute))
	events := []*eventschema.Envelope{
		experimentAssignment(baseID, "experiment:proxy", "baseline", 1, assignedAt),
		experimentAssignment(variantID, "experiment:proxy", "variant", 1, assignedAt),
		stalePrompt,
		staleOutcome,
		basePrompt,
		variantPrompt,
		outcomeEvent(baseID, eventschema.OutcomeAchieved, assignedAt.Add(2*time.Minute)),
		outcomeEvent(variantID, eventschema.OutcomeAchieved, assignedAt.Add(2*time.Minute)),
	}

	got := verify.CompareReconstructedExperiment(nil, events, "experiment:proxy")
	if got.Observational() || got.RandomizedExperimentID != "experiment:proxy" || got.RandomizedPairs != 1 {
		t.Fatalf("proxy-linked trial was not compared: %+v", got)
	}
	if got.BaselineCount != 1 || got.InterventionCount != 1 ||
		got.Comparison.Baseline.AmountOr(-1) != 100 || got.Comparison.Intervention.AmountOr(-1) != 80 {
		t.Fatalf("proxy-linked usage includes stale or misses current prompt: %+v", got.Comparison)
	}
	if got.BaselineOutcomes.Achieved != 1 || got.BaselineOutcomes.NotAchieved != 0 {
		t.Fatalf("pre-assignment outcome contaminated trial: %+v", got.BaselineOutcomes)
	}
	if got.Attributed[0].Execution.Work != "" {
		t.Fatalf("proxy-only evidence invented a work goal: %+v", got.Attributed[0].Execution)
	}
}

func TestRandomizedComparisonFallsBackWhenAnyAssignedOutcomeIsMissing(t *testing.T) {
	execs, events := randomizedFixture(5, "experiment:test", "missing")
	for i, event := range events {
		if event != nil && event.Association.Execution == "missing-variant-3" {
			if _, ok := event.Payload.(*eventschema.OutcomeEvent); ok {
				events = append(events[:i], events[i+1:]...)
				break
			}
		}
	}
	got := verify.Compare(execs, events)
	if !got.Observational() || got.RandomizedExperimentID != "" || got.RandomizedFallbackReason == "" {
		t.Fatalf("incomplete randomized trial was overstated: %+v", got)
	}
}

func TestRandomizedComparisonFallsBackOnPairLedgerGap(t *testing.T) {
	execs, events := randomizedFixture(2, "experiment:gap", "gap")
	kept := events[:0]
	for _, event := range events {
		assignment, ok := event.Payload.(*eventschema.ExperimentEvent)
		if ok && assignment.Pair == 1 {
			continue
		}
		kept = append(kept, event)
	}
	events = kept
	got := verify.Compare(execs, events)
	if !got.Observational() || !strings.Contains(got.RandomizedFallbackReason, "pair ledger") {
		t.Fatalf("gapped assignment ledger was not refused: %+v", got)
	}
}

func TestRandomizedComparisonFallsBackForLegacyUnlinkedAssignments(t *testing.T) {
	execs, events := randomizedFixture(5, "experiment:legacy", "legacy")
	for _, event := range events {
		if assignment, ok := event.Payload.(*eventschema.ExperimentEvent); ok && assignment.Stage == eventschema.ExperimentAssigned {
			event.Association.Execution = ""
			break
		}
	}
	got := verify.Compare(execs, events)
	if !got.Observational() || !strings.Contains(got.RandomizedFallbackReason, "unlinked assignments") {
		t.Fatalf("legacy assignment was included in randomized comparison: %+v", got)
	}
}

func TestRandomizedComparisonRequiresSelectionWhenTrialsAreMixed(t *testing.T) {
	firstExecs, firstEvents := randomizedFixture(5, "experiment:first", "first")
	secondExecs, secondEvents := randomizedFixture(5, "experiment:second", "second")
	execs := append(firstExecs, secondExecs...)
	events := append(firstEvents, secondEvents...)
	got := verify.Compare(execs, events)
	if !got.Observational() || !strings.Contains(got.RandomizedFallbackReason, "multiple experiments") {
		t.Fatalf("mixed trials were not refused: %+v", got)
	}
	selected := verify.CompareExperiment(execs, events, "experiment:first")
	if selected.Observational() || selected.RandomizedExperimentID != "experiment:first" || selected.RandomizedPairs != 5 {
		t.Fatalf("explicit selection failed: %+v", selected)
	}
}

// Events carried an actor and a timestamp; executions carried an actor
// and a span. Nothing joined them, so consumption could never be
// attributed to an attempt at a goal — which is what a comparison needs
// before it can compare anything.
func TestEventsAreAttributedToTheExecutionTheyFallIn(t *testing.T) {
	execs := []work.Execution{
		execution("e1", "session:a", t0, time.Hour),
		execution("e2", "session:a", t0.Add(2*time.Hour), time.Hour),
	}
	events := []*eventschema.Envelope{
		promptEvent("session:a", t0.Add(10*time.Minute), 100),
		promptEvent("session:a", t0.Add(20*time.Minute), 200),
		promptEvent("session:a", t0.Add(2*time.Hour+5*time.Minute), 50),
	}

	got := verify.Attribute(execs, events)

	if len(got) != 2 {
		t.Fatalf("want a record per execution, got %d", len(got))
	}
	if got[0].Tokens.AmountOr(-1) != 300 {
		t.Errorf("e1 tokens = %v, want 300", got[0].Tokens.AmountOr(-1))
	}
	if got[1].Tokens.AmountOr(-1) != 50 {
		t.Errorf("e2 tokens = %v, want 50", got[1].Tokens.AmountOr(-1))
	}
}

// An event belonging to another actor is not this execution's, however
// well its timestamp lines up. Two agents working concurrently is the
// normal case, not the exception.
func TestEventsFromAnotherActorAreNotAttributed(t *testing.T) {
	execs := []work.Execution{execution("e1", "session:a", t0, time.Hour)}
	events := []*eventschema.Envelope{
		promptEvent("session:a", t0.Add(time.Minute), 100),
		promptEvent("session:b", t0.Add(time.Minute), 900),
	}

	got := verify.Attribute(execs, events)
	if got[0].Tokens.AmountOr(-1) != 100 {
		t.Errorf("tokens = %v; another actor's events were counted", got[0].Tokens.AmountOr(-1))
	}
}

func TestExplicitExecutionAssociationOverridesMissingOrDifferentActor(t *testing.T) {
	exec := execution("exec:exact", "session:a", t0, time.Hour)
	matching := promptEvent("", t0.Add(time.Minute), 40)
	matching.Association.Execution = "exec:exact"
	other := promptEvent("session:a", t0.Add(2*time.Minute), 900)
	other.Association.Execution = "exec:other"
	got := verify.Attribute([]work.Execution{exec}, []*eventschema.Envelope{matching, other})
	if tokens := got[0].Tokens.AmountOr(-1); tokens != 40 {
		t.Fatalf("tokens = %v, want only explicitly linked event (40)", tokens)
	}
}

// An execution with no events has unknown consumption, not zero. A
// missing join and a genuinely free attempt are different facts, and
// zero is the one that quietly reads as good news.
func TestAnExecutionWithNoEventsHasUnknownConsumption(t *testing.T) {
	got := verify.Attribute([]work.Execution{execution("e1", "session:a", t0, time.Hour)}, nil)

	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].Tokens.Known() {
		t.Errorf("an unjoined execution reported %v tokens", got[0].Tokens.AmountOr(-1))
	}
	if !strings.Contains(got[0].Tokens.Caveat(), "no events") {
		t.Errorf("the caveat does not say why: %q", got[0].Tokens.Caveat())
	}
}

// An execution is in the intervention cohort when an optimization was
// applied during it. That is the only cohort signal available today,
// and it is observational by construction.
func TestAnAppliedOptimizationMarksTheExecution(t *testing.T) {
	execs := []work.Execution{
		execution("e1", "session:a", t0, time.Hour),
		execution("e2", "session:a", t0.Add(2*time.Hour), time.Hour),
	}
	events := []*eventschema.Envelope{
		promptEvent("session:a", t0.Add(time.Minute), 100),
		optimizationEvent("session:a", t0.Add(2*time.Minute)),
		promptEvent("session:a", t0.Add(2*time.Hour+time.Minute), 100),
	}

	got := verify.Attribute(execs, events)
	if !got[0].Intervened {
		t.Error("an execution with an applied optimization was not marked")
	}
	if got[1].Intervened {
		t.Error("an execution with no optimization was marked")
	}
}

func TestOnlyAppliedDurableDecisionsMarkAnIntervention(t *testing.T) {
	exec := execution("e1", "session:a", t0, time.Hour)
	for _, stage := range []eventschema.DecisionStage{
		eventschema.DecisionStageShadow,
		eventschema.DecisionStageProposed,
		eventschema.DecisionStageRejected,
		eventschema.DecisionStageFailed,
	} {
		got := verify.Attribute([]work.Execution{exec}, []*eventschema.Envelope{
			decisionEvent("session:a", string(stage), "context_trim", stage, t0.Add(time.Minute)),
		})
		if got[0].Intervened {
			t.Errorf("stage %q incorrectly entered intervention cohort", stage)
		}
	}
	got := verify.Attribute([]work.Execution{exec}, []*eventschema.Envelope{
		decisionEvent("session:a", "applied", "context_trim", eventschema.DecisionStageApplied, t0.Add(time.Minute)),
	})
	if !got[0].Intervened || len(got[0].InterventionKinds) != 1 || got[0].InterventionKinds[0] != "context_trim" {
		t.Fatalf("applied durable decision not attributed: %+v", got[0])
	}
}

func TestPairedAppliedDecisionAndOptimizationHaveOneCanonicalKind(t *testing.T) {
	exec := execution("e1", "session:a", t0, time.Hour)
	decision := decisionEvent("session:a", "route-1", "model_route", eventschema.DecisionStageApplied, t0.Add(time.Minute))
	optimization := optimizationEvent("session:a", t0.Add(2*time.Minute))
	optimization.Correlation = decision.Correlation
	got := verify.Attribute([]work.Execution{exec}, []*eventschema.Envelope{decision, optimization})
	if !got[0].Intervened || len(got[0].InterventionKinds) != 1 || got[0].InterventionKinds[0] != "model_route" {
		t.Fatalf("paired decision double-counted: %+v", got[0].InterventionKinds)
	}
}

func TestExplicitOutcomeJoinsByExecutionEvenWhenRecordedLater(t *testing.T) {
	exec := execution("e1", "session:a", t0, time.Hour)
	got := verify.Attribute([]work.Execution{exec}, []*eventschema.Envelope{
		promptEvent("session:a", t0.Add(time.Minute), 100),
		outcomeEvent("e1", eventschema.OutcomeAchieved, exec.EndedAt.Add(24*time.Hour)),
	})
	if got[0].Outcomes.Achieved != 1 || got[0].Outcomes.Unknown != 0 {
		t.Fatalf("outcomes = %+v; outcome should join by execution id", got[0].Outcomes)
	}
}

func TestComparisonUsesOutcomeRegressionToFlagHarm(t *testing.T) {
	execs := make([]work.Execution, 0, 20)
	events := make([]*eventschema.Envelope, 0, 50)
	for i := range 20 {
		at := t0.Add(time.Duration(i) * 2 * time.Hour)
		id := fmt.Sprintf("execution-%d", i)
		exec := execution(id, "session:a", at, time.Hour)
		execs = append(execs, exec)
		intervened := i >= 10
		tokens, result := int64(1000), eventschema.OutcomeAchieved
		if intervened {
			tokens, result = 600, eventschema.OutcomeNotAchieved
			events = append(events, optimizationEvent("session:a", at.Add(time.Minute)))
		}
		events = append(events,
			promptEvent("session:a", at.Add(2*time.Minute), tokens),
			outcomeEvent(id, result, exec.EndedAt.Add(time.Hour)),
		)
	}
	report := verify.Compare(execs, events)
	if report.Verdict.Outcome != intervention.Harmed {
		t.Fatalf("verdict = %+v; token reduction with collapsed success must be harmful", report.Verdict)
	}
	base, baseOK := report.BaselineOutcomes.SuccessRate.Amount()
	with, withOK := report.InterventionOutcomes.SuccessRate.Amount()
	if !baseOK || !withOK || base != 100 || with != 0 {
		t.Fatalf("success rates = %.1f%% (%v) / %.1f%% (%v)", base, baseOK, with, withOK)
	}
	if !report.Observational() || !strings.Contains(report.Verdict.Caveat, "not assigned") {
		t.Fatalf("outcome harm must disclose observational assignment: %+v", report.Verdict)
	}
}

// The whole point, and the line this capability must not cross.
// Splitting by whether an optimization fired is observational, so the
// comparison it builds says so and the verdict cannot claim the
// intervention helped.
func TestTheComparisonItBuildsIsObservational(t *testing.T) {
	execs := make([]work.Execution, 0, 20)
	var events []*eventschema.Envelope
	// Ten attempts with an optimization, ten without.
	for i := range 20 {
		at := t0.Add(time.Duration(i) * 2 * time.Hour)
		id := string(rune('a' + i))
		execs = append(execs, execution(id, "session:a", at, time.Hour))
		tokens := int64(1000)
		if i%2 == 0 {
			tokens = 600
			events = append(events, optimizationEvent("session:a", at.Add(time.Minute)))
		}
		events = append(events, promptEvent("session:a", at.Add(2*time.Minute), tokens))
	}

	report := verify.Compare(execs, events)

	if report.Comparison.Assignment != intervention.Observational {
		t.Errorf("assignment = %q, want observational", report.Comparison.Assignment)
	}
	if report.Verdict.Outcome == intervention.Improved {
		t.Error("an observational comparison claimed the intervention helped")
	}
	if !strings.Contains(report.Verdict.Caveat, "not assigned") {
		t.Errorf("the verdict does not disclose the confound: %q", report.Verdict.Caveat)
	}
	// The difference is still reported — it is the reason to run a real
	// experiment.
	if _, ok := report.Verdict.Observed.Amount(); !ok {
		t.Error("the measured difference was withheld")
	}
}

// Too few attempts on either side conclude nothing, and say which side
// was short — "not enough data" without saying which is advice nobody
// can act on.
func TestTooFewInEitherCohortIsReported(t *testing.T) {
	execs := []work.Execution{execution("e1", "session:a", t0, time.Hour)}
	events := []*eventschema.Envelope{promptEvent("session:a", t0.Add(time.Minute), 100)}

	report := verify.Compare(execs, events)
	if report.Verdict.Conclusive() {
		t.Error("one execution produced a conclusive verdict")
	}
	if report.BaselineCount != 1 || report.InterventionCount != 0 {
		t.Errorf("cohort counts = %d/%d", report.BaselineCount, report.InterventionCount)
	}
}

func TestNothingToCompareIsNotAnError(t *testing.T) {
	report := verify.Compare(nil, nil)
	if report.Verdict.Conclusive() {
		t.Error("an empty comparison concluded something")
	}
}

// "0 executions is not enough; 5 are needed" is the wrong diagnosis when
// there were no attempts at all. The operator's problem is that nothing
// was reconstructed — no transcripts, or none inside the window — not
// that they need more data of a kind they are already collecting.
//
// Sending someone to wait for more samples when the pipeline produced
// none is the same class of unhelpful answer as reporting a refused
// poller as an unused vendor.
func TestNoAttemptsSaysNothingWasReconstructed(t *testing.T) {
	report := verify.Compare(nil, nil)

	if !strings.Contains(report.Verdict.Caveat, "no attempts") {
		t.Errorf("the caveat blames sample size rather than the empty "+
			"reconstruction: %q", report.Verdict.Caveat)
	}
}

// With attempts but none in one cohort, sample size genuinely is the
// problem and the message should say which side was short.
func TestAttemptsInOnlyOneCohortSaysWhichSide(t *testing.T) {
	execs := []work.Execution{
		execution("e1", "session:a", t0, time.Hour),
		execution("e2", "session:a", t0.Add(2*time.Hour), time.Hour),
	}
	events := []*eventschema.Envelope{
		promptEvent("session:a", t0.Add(time.Minute), 100),
		promptEvent("session:a", t0.Add(2*time.Hour+time.Minute), 100),
	}

	report := verify.Compare(execs, events)
	if strings.Contains(report.Verdict.Caveat, "no attempts") {
		t.Errorf("two attempts were reported as none: %q", report.Verdict.Caveat)
	}
	if !strings.Contains(report.Verdict.Caveat, "2 attempt") {
		t.Errorf("the caveat does not name the cohort sizes: %q", report.Verdict.Caveat)
	}
}
