package optimizer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// fakeOpt is a controllable Optimizer used to assert pipeline behaviour.
type fakeOpt struct {
	kind  eventschema.OptimizationType
	recs  []Recommendation
	err   error
	calls int
	delay time.Duration
}

func (f *fakeOpt) Kind() eventschema.OptimizationType { return f.kind }

func (f *fakeOpt) Run(_ context.Context, _ *Request) ([]Recommendation, error) {
	f.calls++
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.recs, f.err
}

// fakeClock advances by step on each call, returning predictable
// timestamps so latency-budget tests are deterministic.
type fakeClock struct {
	now  time.Time
	step time.Duration
}

func (c *fakeClock) Now() time.Time {
	t := c.now
	c.now = c.now.Add(c.step)
	return t
}

func TestPassiveModeEmitsSkippedAndKeepsBody(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypePromptCompress,
		recs: []Recommendation{{
			Kind:                   eventschema.OptimizationTypePromptCompress,
			EstimatedSavingsTokens: 50,
			QualityScore:           0.95,
			ApplyBody:              []byte("rewritten"),
		}},
	}
	p := NewPipeline(opt)
	res, err := p.Run(context.Background(), &Request{
		PromptHash: "h",
		Body:       []byte("original"),
		Mode:       ModePassive,
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Body) != "original" {
		t.Errorf("passive mutated body: %q", res.Body)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(res.Events))
	}
	if res.Events[0].Decision != eventschema.OptimizationDecisionSkipped {
		t.Errorf("decision = %s, want skipped", res.Events[0].Decision)
	}
	if res.Events[0].Mode != ModePassive {
		t.Errorf("mode lost: %s", res.Events[0].Mode)
	}
	if res.Events[0].EstimatedSavingsTokens != 50 {
		t.Errorf("savings lost: %d", res.Events[0].EstimatedSavingsTokens)
	}
}

func TestInteractiveModeAppliesAcceptedBody(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypeContextTrim,
		recs: []Recommendation{{
			Kind:      eventschema.OptimizationTypeContextTrim,
			ApplyBody: []byte("trimmed"),
		}},
	}
	p := NewPipeline(opt)
	decider := func(_ context.Context, _ Recommendation) (bool, error) { return true, nil }
	res, err := p.Run(context.Background(), &Request{
		Body: []byte("original"), Mode: ModeInteractive,
	}, decider)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Body) != "trimmed" {
		t.Errorf("body not applied: %q", res.Body)
	}
	if res.Events[0].Decision != eventschema.OptimizationDecisionApplied {
		t.Errorf("decision = %s", res.Events[0].Decision)
	}
}

func TestInteractiveModeRejectionLeavesBodyAndMarks(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypeRouter,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypeRouter, ApplyBody: []byte("changed")}},
	}
	p := NewPipeline(opt)
	decider := func(_ context.Context, _ Recommendation) (bool, error) { return false, nil }
	res, _ := p.Run(context.Background(), &Request{
		Body: []byte("original"), Mode: ModeInteractive,
	}, decider)
	if string(res.Body) != "original" {
		t.Errorf("rejected rec mutated body: %q", res.Body)
	}
	if res.Events[0].Decision != eventschema.OptimizationDecisionRejected {
		t.Errorf("decision = %s", res.Events[0].Decision)
	}
}

func TestInteractiveWithoutDeciderRejects(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypeDedupe,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypeDedupe, ApplyBody: []byte("x")}},
	}
	p := NewPipeline(opt)
	res, _ := p.Run(context.Background(), &Request{Body: []byte("y"), Mode: ModeInteractive}, nil)
	if res.Events[0].Decision != eventschema.OptimizationDecisionRejected {
		t.Errorf("interactive without decider should reject, got %s", res.Events[0].Decision)
	}
}

func TestReplayModeRecordsButDoesNotApply(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypePromptCompress,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypePromptCompress, ApplyBody: []byte("compressed")}},
	}
	p := NewPipeline(opt)
	res, _ := p.Run(context.Background(), &Request{Body: []byte("original"), Mode: ModeReplay}, nil)
	if string(res.Body) != "original" {
		t.Errorf("replay mutated body: %q", res.Body)
	}
	if res.Events[0].Decision != eventschema.OptimizationDecisionSkipped {
		t.Errorf("decision = %s", res.Events[0].Decision)
	}
}

func TestErrorIsRecordedAsSkipped(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypeRetrievalPrune,
		err:  errors.New("boom"),
	}
	p := NewPipeline(opt)
	res, _ := p.Run(context.Background(), &Request{Mode: ModePassive}, nil)
	if len(res.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(res.Events))
	}
	if res.Events[0].Decision != eventschema.OptimizationDecisionSkipped {
		t.Errorf("decision = %s", res.Events[0].Decision)
	}
	if res.Events[0].Reason == "" {
		t.Errorf("reason empty for error event")
	}
}

func TestOrderingPreserved(t *testing.T) {
	first := &fakeOpt{kind: eventschema.OptimizationTypePromptCompress,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypePromptCompress}}}
	second := &fakeOpt{kind: eventschema.OptimizationTypeContextTrim,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypeContextTrim}}}
	p := NewPipeline(first, second)
	res, _ := p.Run(context.Background(), &Request{Mode: ModePassive}, nil)
	if len(res.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(res.Events))
	}
	if res.Events[0].Kind != eventschema.OptimizationTypePromptCompress {
		t.Errorf("first event kind = %s", res.Events[0].Kind)
	}
	if res.Events[1].Kind != eventschema.OptimizationTypeContextTrim {
		t.Errorf("second event kind = %s", res.Events[1].Kind)
	}
}

func TestLatencyBudgetSkipsLater(t *testing.T) {
	first := &fakeOpt{kind: eventschema.OptimizationTypePromptCompress,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypePromptCompress}}}
	second := &fakeOpt{kind: eventschema.OptimizationTypeContextTrim,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypeContextTrim}}}
	third := &fakeOpt{kind: eventschema.OptimizationTypeRetrievalPrune}

	// fakeClock advances 10ms per call. Budget is 25ms; check happens
	// before each optimizer's stageStart (1 call) plus elapsed read.
	clock := &fakeClock{now: time.Unix(0, 0), step: 10 * time.Millisecond}
	p := NewPipeline(first, second, third)
	p.SetClock(clock.Now)

	res, err := p.Run(context.Background(), &Request{
		Mode:          ModePassive,
		LatencyBudget: 25 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.BudgetHit {
		t.Errorf("BudgetHit = false, want true")
	}
	// Expect at least one budget-skipped event for the late-stage optimizers.
	skipReasons := 0
	for _, e := range res.Events {
		if e.Reason == "latency_budget_exceeded" {
			skipReasons++
		}
	}
	if skipReasons == 0 {
		t.Errorf("expected at least one latency_budget_exceeded event, got: %+v", res.Events)
	}
	if third.calls != 0 {
		t.Errorf("third optimizer should have been skipped, got %d calls", third.calls)
	}
}

func TestNilRequestReturnsError(t *testing.T) {
	p := NewPipeline()
	if _, err := p.Run(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on nil request")
	}
}

func TestEmptyPipelineReturnsBodyUnchanged(t *testing.T) {
	p := NewPipeline()
	res, err := p.Run(context.Background(), &Request{Body: []byte("x"), Mode: ModePassive}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Body) != "x" {
		t.Errorf("body changed: %q", res.Body)
	}
	if len(res.Events) != 0 {
		t.Errorf("events = %d, want 0", len(res.Events))
	}
}

func TestRecommendationKindFallsBackToOptimizerKind(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypeCacheReuse,
		recs: []Recommendation{{}}, // Kind unset
	}
	p := NewPipeline(opt)
	res, _ := p.Run(context.Background(), &Request{Mode: ModePassive}, nil)
	if res.Events[0].Kind != eventschema.OptimizationTypeCacheReuse {
		t.Errorf("kind fallback lost: %s", res.Events[0].Kind)
	}
}

func TestDeciderAcceptedWithoutBodyMarksAccepted(t *testing.T) {
	opt := &fakeOpt{
		kind: eventschema.OptimizationTypeRouter,
		recs: []Recommendation{{Kind: eventschema.OptimizationTypeRouter}}, // no ApplyBody
	}
	p := NewPipeline(opt)
	decider := func(_ context.Context, _ Recommendation) (bool, error) { return true, nil }
	res, _ := p.Run(context.Background(), &Request{Body: []byte("x"), Mode: ModeInteractive}, decider)
	if res.Events[0].Decision != eventschema.OptimizationDecisionAccepted {
		t.Errorf("decision = %s, want accepted", res.Events[0].Decision)
	}
	if string(res.Body) != "x" {
		t.Errorf("body should be unchanged: %q", res.Body)
	}
}
