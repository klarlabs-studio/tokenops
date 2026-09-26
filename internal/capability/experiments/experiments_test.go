package experiments

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type memoryLedger struct{ events []*eventschema.Envelope }

func (m *memoryLedger) Append(_ context.Context, e *eventschema.Envelope) error {
	m.events = append(m.events, e)
	return nil
}
func (m *memoryLedger) ExperimentEvents(_ context.Context, id string) ([]*eventschema.Envelope, error) {
	if id == "" {
		return append([]*eventschema.Envelope(nil), m.events...), nil
	}
	var out []*eventschema.Envelope
	for _, e := range m.events {
		if e.Correlation.Experiment == id {
			out = append(out, e)
		}
	}
	return out, nil
}

func TestTrialIsBoundedPairedAndPersistent(t *testing.T) {
	ctx := context.Background()
	ledger := &memoryLedger{}
	m := New(ledger)
	now := time.Unix(100, 0).UTC()
	state, err := m.Start(ctx, StartInput{
		Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", MaxPairs: 1, At: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []eventschema.ExperimentGuardrail{{Metric: "quality"}, {Metric: "latency_ms", MaxRegressionPct: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := m.Assign(ctx, AssignmentInput{Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", ExecutionID: "e1", At: now})
	if err != nil || !ok {
		t.Fatalf("first assignment: %+v %v %v", a, ok, err)
	}
	reused, ok, err := m.Assign(ctx, AssignmentInput{Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", ExecutionID: "e1", At: now})
	if err != nil || !ok || reused != a || len(ledger.events) != 2 {
		t.Fatalf("repeated request changed execution arm: first=%+v repeated=%+v ok=%v err=%v events=%d", a, reused, ok, err, len(ledger.events))
	}
	b, ok, err := m.Assign(ctx, AssignmentInput{Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", ExecutionID: "e2", At: now})
	if err != nil || !ok || a.Variant == b.Variant || a.Pair != b.Pair {
		t.Fatalf("pair = %+v %+v, ok=%v err=%v", a, b, ok, err)
	}
	if _, ok, err := m.Assign(ctx, AssignmentInput{Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", ExecutionID: "e3", At: now}); err != nil || ok {
		t.Fatalf("trial exceeded bound: ok=%v err=%v", ok, err)
	}
	for execution, want := range map[string]Assignment{"e1": a, "e2": b} {
		got, reused, err := m.Assign(ctx, AssignmentInput{Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", ExecutionID: execution, At: now.Add(time.Hour)})
		if err != nil || !reused || got != want || len(ledger.events) != 3 {
			t.Fatalf("completed trial lost %s assignment: got=%+v want=%+v reused=%v err=%v events=%d", execution, got, want, reused, err, len(ledger.events))
		}
	}
	got, ok, err := m.Status(ctx, state.ID, now)
	if err != nil || !ok || got.Stage != eventschema.ExperimentCompleted {
		t.Fatalf("state = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestFingerprintDriftPreventsAssignment(t *testing.T) {
	ctx := context.Background()
	ledger := &memoryLedger{}
	m := New(ledger)
	now := time.Unix(100, 0).UTC()
	_, err := m.Start(ctx, StartInput{
		Provider: "openai", BaselineModel: "large", VariantModel: "mini", Fingerprint: "old", At: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []eventschema.ExperimentGuardrail{{Metric: "quality"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := m.Assign(ctx, AssignmentInput{Provider: "openai", BaselineModel: "large", VariantModel: "mini", Fingerprint: "new", At: now})
	if err != nil || ok {
		t.Fatalf("drift assigned: ok=%v err=%v", ok, err)
	}
}

func TestAssignmentRequiresExecutionIdentity(t *testing.T) {
	ctx := context.Background()
	ledger := &memoryLedger{}
	m := New(ledger)
	now := time.Unix(100, 0).UTC()
	if _, err := m.Start(ctx, StartInput{
		Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", At: now,
		ObjectiveMetric: "tokens", MinImprovementPct: 10,
		Guardrails: []eventschema.ExperimentGuardrail{{Metric: "quality"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, assigned, err := m.Assign(ctx, AssignmentInput{
		Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet", At: now,
	}); err != nil || assigned {
		t.Fatalf("assignment without execution identity = assigned %v, err %v", assigned, err)
	}
	if got := len(ledger.events); got != 1 {
		t.Fatalf("unjoinable assignment was persisted: %d events", got)
	}
}

func TestTrialRequiresExplicitUtilityPolicy(t *testing.T) {
	m := New(&memoryLedger{})
	_, err := m.Start(context.Background(), StartInput{
		Provider: "anthropic", BaselineModel: "opus", VariantModel: "sonnet",
	})
	if err == nil {
		t.Fatal("trial without objective and guardrails was accepted")
	}
}
