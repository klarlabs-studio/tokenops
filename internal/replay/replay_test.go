package replay

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/spend"
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

func mkPrompt(id, wf string, ts time.Time, _ string, in, out int64, cost float64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: ts, Source: "replay-test",
		Payload: &eventschema.PromptEvent{
			PromptHash: "h-" + id, Provider: eventschema.ProviderOpenAI,
			RequestModel: "gpt-4o-mini", InputTokens: in, OutputTokens: out, TotalTokens: in + out,
			CostUSD: cost, WorkflowID: wf,
		},
	}
}

// fixedRecommender is an Optimizer that always returns the same
// recommendation, regardless of request — a clean fake for replay tests.
type fixedRecommender struct {
	kind        eventschema.OptimizationType
	tokenSaving int64
	usdSaving   float64
	quality     float64
}

func (f *fixedRecommender) Kind() eventschema.OptimizationType { return f.kind }

func (f *fixedRecommender) Run(_ context.Context, _ *optimizer.Request) ([]optimizer.Recommendation, error) {
	return []optimizer.Recommendation{{
		Kind:                   f.kind,
		EstimatedSavingsTokens: f.tokenSaving,
		EstimatedSavingsUSD:    f.usdSaving,
		QualityScore:           f.quality,
	}}, nil
}

func TestReplayLoadsSessionAndComputesSavings(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("a", "wf-1", base, "gpt-4o-mini", 1000, 500, 0.001),
		mkPrompt("b", "wf-1", base.Add(time.Minute), "gpt-4o-mini", 2000, 1000, 0.002),
		mkPrompt("c", "wf-2", base.Add(time.Minute), "gpt-4o-mini", 3000, 1500, 0.003),
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	pipe := optimizer.NewPipeline(&fixedRecommender{
		kind: eventschema.OptimizationTypePromptCompress,
		// 100 tokens saved, $0.01 saved per recommendation.
		tokenSaving: 100,
		usdSaving:   0.01,
		quality:     0.95,
	})
	eng := New(store, pipe, nil)
	res, err := eng.Replay(ctx, SessionSelector{WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
	if res.OriginalCostUSD != 0.003 {
		t.Errorf("original cost = %.4f, want 0.003", res.OriginalCostUSD)
	}
	if res.EstimatedSavingsTokens != 200 {
		t.Errorf("savings tokens = %d, want 200", res.EstimatedSavingsTokens)
	}
	if res.EstimatedSavingsUSD != 0.02 {
		t.Errorf("savings USD = %f, want 0.02", res.EstimatedSavingsUSD)
	}
}

func TestReplayRecomputesOriginalCostWhenZero(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	// CostUSD left at 0 — replay should recompute via spend.Engine.
	env := mkPrompt("x", "wf-1", base, "gpt-4o-mini", 1_000_000, 1_000_000, 0)
	_ = store.Append(ctx, env)

	pipe := optimizer.NewPipeline()
	eng := New(store, pipe, spend.NewEngine(spend.DefaultTable()))
	res, err := eng.Replay(ctx, SessionSelector{WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.OriginalCostUSD < 0.7 || res.OriginalCostUSD > 0.8 {
		t.Errorf("recomputed original cost out of band: %.4f", res.OriginalCostUSD)
	}
}

func TestReplayTokenSavingsToUSDViaSpendEngine(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	env := mkPrompt("a", "wf-1", base, "gpt-4o-mini", 1_000_000, 0, 0.15)
	_ = store.Append(ctx, env)

	// Recommendation reports token savings without a USD estimate.
	pipe := optimizer.NewPipeline(&fixedRecommender{
		kind: eventschema.OptimizationTypeContextTrim, tokenSaving: 1_000_000,
	})
	eng := New(store, pipe, spend.NewEngine(spend.DefaultTable()))
	res, err := eng.Replay(ctx, SessionSelector{WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	// 1M input tokens at gpt-4o-mini input rate $0.15/M.
	if res.EstimatedSavingsUSD < 0.14 || res.EstimatedSavingsUSD > 0.16 {
		t.Errorf("token-to-USD savings off: %f", res.EstimatedSavingsUSD)
	}
}

func TestReplayEmptySession(t *testing.T) {
	store := newStore(t)
	pipe := optimizer.NewPipeline()
	eng := New(store, pipe, nil)
	_, err := eng.Replay(context.Background(), SessionSelector{WorkflowID: "missing"})
	if !errors.Is(err, ErrEmptySession) {
		t.Errorf("err = %v, want ErrEmptySession", err)
	}
}

func TestSavingsRatio(t *testing.T) {
	r := Result{OriginalCostUSD: 1.0, EstimatedSavingsUSD: 0.25}
	if got := r.SavingsRatio(); got != 0.25 {
		t.Errorf("ratio = %f", got)
	}
	zero := Result{}
	if got := zero.SavingsRatio(); got != 0 {
		t.Errorf("zero original ratio = %f", got)
	}
}

func TestReplayRunsInReplayMode(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	env := mkPrompt("a", "wf-1", time.Now().UTC(), "gpt-4o-mini", 100, 50, 0.001)
	_ = store.Append(ctx, env)

	rec := &fixedRecommender{kind: eventschema.OptimizationTypePromptCompress, tokenSaving: 10}
	pipe := optimizer.NewPipeline(rec)
	eng := New(store, pipe, nil)
	res, err := eng.Replay(ctx, SessionSelector{WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("steps = %d", len(res.Steps))
	}
	ev := res.Steps[0].OptimizationEvents
	if len(ev) != 1 {
		t.Fatalf("events = %d", len(ev))
	}
	// In replay mode, optimizer events are stamped Skipped (record-only).
	if ev[0].Decision != eventschema.OptimizationDecisionSkipped {
		t.Errorf("decision = %s, want skipped", ev[0].Decision)
	}
	if ev[0].Mode != optimizer.ModeReplay {
		t.Errorf("mode = %s, want replay", ev[0].Mode)
	}
}

func TestReplayBySessionAndAgent(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		{
			ID: "a", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: base, Source: "test",
			Payload: &eventschema.PromptEvent{
				PromptHash: "h", Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
				InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostUSD: 0.01,
				SessionID: "sess-A", AgentID: "agent-1",
			},
		},
		{
			ID: "b", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: base, Source: "test",
			Payload: &eventschema.PromptEvent{
				PromptHash: "h", Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
				InputTokens: 20, OutputTokens: 5, TotalTokens: 25, CostUSD: 0.02,
				SessionID: "sess-B", AgentID: "agent-2",
			},
		},
	}
	_ = store.AppendBatch(ctx, envs)
	pipe := optimizer.NewPipeline()
	eng := New(store, pipe, nil)

	res, err := eng.Replay(ctx, SessionSelector{SessionID: "sess-A"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].OriginalEnvelope.ID != "a" {
		t.Errorf("session filter: %+v", res.Steps)
	}

	res, err = eng.Replay(ctx, SessionSelector{AgentID: "agent-2"})
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].OriginalEnvelope.ID != "b" {
		t.Errorf("agent filter: %+v", res.Steps)
	}
}
