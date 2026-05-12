package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// wf is the fixed workflow id used by all test fixtures; mkStep accepts
// it as a parameter for readability rather than for variation.
func mkStep(id, wf, agent, model string, ts time.Time, in, out int64, cost float64, latency time.Duration) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: ts, Source: "wf-test",
		Payload: &eventschema.PromptEvent{
			PromptHash: "h-" + id, Provider: eventschema.ProviderOpenAI,
			RequestModel: model, InputTokens: in, OutputTokens: out, TotalTokens: in + out,
			CostUSD: cost, Latency: latency,
			WorkflowID: wf, AgentID: agent,
		},
	}
}

func TestReconstructOrdersByTimestamp(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkStep("s2", "wf-A", "agent", "gpt-4o", base.Add(time.Minute), 200, 100, 0.002, 100*time.Millisecond),
		mkStep("s1", "wf-A", "agent", "gpt-4o", base, 100, 50, 0.001, 80*time.Millisecond),
		mkStep("s3", "wf-A", "agent", "gpt-4o", base.Add(2*time.Minute), 350, 150, 0.004, 120*time.Millisecond),
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	tr, err := Reconstruct(ctx, store, nil, "wf-A")
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if tr.StepCount != 3 {
		t.Fatalf("steps = %d", tr.StepCount)
	}
	if tr.Steps[0].Envelope.ID != "s1" || tr.Steps[1].Envelope.ID != "s2" || tr.Steps[2].Envelope.ID != "s3" {
		t.Errorf("ordering wrong: %+v", []string{tr.Steps[0].Envelope.ID, tr.Steps[1].Envelope.ID, tr.Steps[2].Envelope.ID})
	}
}

func TestReconstructComputesContextGrowth(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkStep("a", "wf-A", "agent", "gpt-4o", base, 100, 50, 0.001, 0),
		mkStep("b", "wf-A", "agent", "gpt-4o", base.Add(time.Minute), 250, 80, 0.002, 0),    // +150
		mkStep("c", "wf-A", "agent", "gpt-4o", base.Add(2*time.Minute), 300, 100, 0.003, 0), // +50
		mkStep("d", "wf-A", "agent", "gpt-4o", base.Add(3*time.Minute), 200, 50, 0.001, 0),  // -100 (no growth credited)
	}
	_ = store.AppendBatch(ctx, envs)
	tr, _ := Reconstruct(ctx, store, nil, "wf-A")
	if tr.ContextGrowthTotal != 200 {
		t.Errorf("growth = %d, want 200", tr.ContextGrowthTotal)
	}
	if tr.MaxContextSize != 300 {
		t.Errorf("max context = %d, want 300", tr.MaxContextSize)
	}
}

func TestReconstructTotals(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkStep("a", "wf-A", "agent-1", "gpt-4o", base, 100, 50, 0.001, 0),
		mkStep("b", "wf-A", "agent-2", "gpt-4o-mini", base.Add(time.Minute), 200, 100, 0.002, 0),
	}
	_ = store.AppendBatch(ctx, envs)
	tr, _ := Reconstruct(ctx, store, nil, "wf-A")
	if tr.TotalInputTokens != 300 || tr.TotalOutputTokens != 150 || tr.TotalTotalTokens != 450 {
		t.Errorf("totals: %+v", tr)
	}
	if tr.TotalCostUSD < 0.0029 || tr.TotalCostUSD > 0.0031 {
		t.Errorf("cost = %f", tr.TotalCostUSD)
	}
	if tr.Models["gpt-4o"] != 1 || tr.Models["gpt-4o-mini"] != 1 {
		t.Errorf("models: %+v", tr.Models)
	}
	if tr.Agents["agent-1"] != 1 || tr.Agents["agent-2"] != 1 {
		t.Errorf("agents: %+v", tr.Agents)
	}
}

func TestReconstructRecomputesCostWhenZero(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	env := mkStep("a", "wf-A", "agent", "gpt-4o-mini",
		time.Now().UTC(), 1_000_000, 1_000_000, 0, 0)
	_ = store.Append(ctx, env)
	tr, err := Reconstruct(ctx, store, spend.NewEngine(spend.DefaultTable()), "wf-A")
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if tr.TotalCostUSD < 0.7 || tr.TotalCostUSD > 0.8 {
		t.Errorf("recomputed cost out of band: %f", tr.TotalCostUSD)
	}
}

func TestReconstructEmpty(t *testing.T) {
	store := newStore(t)
	_, err := Reconstruct(context.Background(), store, nil, "missing")
	if !errors.Is(err, ErrNoTrace) {
		t.Errorf("err = %v", err)
	}
}

func TestSummarize(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkStep("a", "wf-A", "agent-1", "gpt-4o", base, 100, 50, 0.001, 0),
		mkStep("b", "wf-A", "agent-1", "gpt-4o", base.Add(time.Minute), 250, 100, 0.002, 0),
	}
	_ = store.AppendBatch(ctx, envs)
	tr, _ := Reconstruct(ctx, store, nil, "wf-A")
	s := tr.Summarize()
	if s.StepCount != 2 || s.TotalTokens != 500 {
		t.Errorf("summary: %+v", s)
	}
	if s.UniqueModels != 1 || s.UniqueAgents != 1 {
		t.Errorf("uniques: %+v", s)
	}
	if s.Duration != time.Minute {
		t.Errorf("duration = %s", s.Duration)
	}
}

func TestNilStore(t *testing.T) {
	_, err := Reconstruct(context.Background(), nil, nil, "x")
	if err == nil {
		t.Error("expected nil store error")
	}
}

func TestEmptyWorkflowID(t *testing.T) {
	store := newStore(t)
	_, err := Reconstruct(context.Background(), store, nil, "")
	if err == nil {
		t.Error("expected empty id error")
	}
}

func TestStartGapBetweenSteps(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkStep("a", "wf-A", "agent", "gpt-4o", base, 1, 1, 0, 0),
		mkStep("b", "wf-A", "agent", "gpt-4o", base.Add(30*time.Second), 1, 1, 0, 0),
	}
	_ = store.AppendBatch(ctx, envs)
	tr, _ := Reconstruct(ctx, store, nil, "wf-A")
	if tr.Steps[1].StartGap != 30*time.Second {
		t.Errorf("start gap = %s, want 30s", tr.Steps[1].StartGap)
	}
}
