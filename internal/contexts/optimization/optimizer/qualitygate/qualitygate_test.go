package qualitygate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

type fakeOpt struct {
	kind eventschema.OptimizationType
	recs []optimizer.Recommendation
	err  error
}

func (f *fakeOpt) Kind() eventschema.OptimizationType { return f.kind }
func (f *fakeOpt) Run(_ context.Context, _ *optimizer.Request) ([]optimizer.Recommendation, error) {
	out := make([]optimizer.Recommendation, len(f.recs))
	copy(out, f.recs)
	return out, f.err
}

func TestDeciderAcceptsHighQuality(t *testing.T) {
	dec := NewDecider(0.8, nil)
	ok, err := dec(context.Background(), optimizer.Recommendation{QualityScore: 0.9})
	if err != nil {
		t.Fatalf("dec: %v", err)
	}
	if !ok {
		t.Errorf("expected accept for QualityScore=0.9, threshold=0.8")
	}
}

func TestDeciderRejectsLowQuality(t *testing.T) {
	dec := NewDecider(0.8, nil)
	ok, err := dec(context.Background(), optimizer.Recommendation{QualityScore: 0.7})
	if err != nil {
		t.Fatalf("dec: %v", err)
	}
	if ok {
		t.Errorf("expected reject for QualityScore=0.7, threshold=0.8")
	}
}

func TestDeciderDelegatesToInner(t *testing.T) {
	innerCalled := false
	inner := func(_ context.Context, _ optimizer.Recommendation) (bool, error) {
		innerCalled = true
		return false, nil
	}
	dec := NewDecider(0.5, inner)
	ok, _ := dec(context.Background(), optimizer.Recommendation{QualityScore: 0.9})
	if !innerCalled {
		t.Errorf("inner decider not invoked above threshold")
	}
	if ok {
		t.Errorf("expected inner reject to propagate")
	}
}

func TestDeciderDoesNotInvokeInnerBelowThreshold(t *testing.T) {
	inner := func(_ context.Context, _ optimizer.Recommendation) (bool, error) {
		t.Errorf("inner decider should not be invoked below threshold")
		return true, nil
	}
	dec := NewDecider(0.8, inner)
	if ok, _ := dec(context.Background(), optimizer.Recommendation{QualityScore: 0.5}); ok {
		t.Errorf("expected reject below threshold")
	}
}

func TestDeciderDefaultThreshold(t *testing.T) {
	dec := NewDecider(0, nil)
	ok, _ := dec(context.Background(), optimizer.Recommendation{QualityScore: DefaultThreshold - 0.01})
	if ok {
		t.Errorf("expected reject just below DefaultThreshold")
	}
	ok, _ = dec(context.Background(), optimizer.Recommendation{QualityScore: DefaultThreshold + 0.01})
	if !ok {
		t.Errorf("expected accept just above DefaultThreshold")
	}
}

func TestWrapClearsApplyBodyOnLowQuality(t *testing.T) {
	inner := &fakeOpt{
		kind: eventschema.OptimizationTypePromptCompress,
		recs: []optimizer.Recommendation{
			{Kind: eventschema.OptimizationTypePromptCompress, QualityScore: 0.95, ApplyBody: []byte("good")},
			{Kind: eventschema.OptimizationTypePromptCompress, QualityScore: 0.5, ApplyBody: []byte("risky"), Reason: "trim"},
		},
	}
	gated := Wrap(inner, 0.8)
	recs, err := gated.Run(context.Background(), &optimizer.Request{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("recs = %d", len(recs))
	}
	if string(recs[0].ApplyBody) != "good" {
		t.Errorf("high-quality body lost: %q", recs[0].ApplyBody)
	}
	if recs[1].ApplyBody != nil {
		t.Errorf("low-quality body retained: %q", recs[1].ApplyBody)
	}
	if !strings.Contains(recs[1].Reason, reasonPrefix) {
		t.Errorf("reason not annotated: %q", recs[1].Reason)
	}
	if !strings.Contains(recs[1].Reason, "trim") {
		t.Errorf("original reason lost: %q", recs[1].Reason)
	}
}

func TestWrapPropagatesError(t *testing.T) {
	inner := &fakeOpt{kind: eventschema.OptimizationTypePromptCompress, err: errors.New("boom")}
	gated := Wrap(inner, 0.8)
	if _, err := gated.Run(context.Background(), &optimizer.Request{}); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestWrapKindMatchesInner(t *testing.T) {
	inner := &fakeOpt{kind: eventschema.OptimizationTypeContextTrim}
	if got := Wrap(inner, 0.8).Kind(); got != eventschema.OptimizationTypeContextTrim {
		t.Errorf("kind mismatch: %s", got)
	}
}

func TestAnnotateReasonEmpty(t *testing.T) {
	got := annotateReason("", 0.8, 0.5)
	if !strings.Contains(got, "score=0.50") || !strings.Contains(got, "min=0.80") {
		t.Errorf("reason format: %q", got)
	}
}
