package efficiency

import (
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/workflow"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func mkStep(agent, model string, in, out int64) workflow.Step {
	return workflow.Step{
		Prompt: &eventschema.PromptEvent{
			Provider: eventschema.ProviderOpenAI, RequestModel: model,
			AgentID: agent, InputTokens: in, OutputTokens: out, TotalTokens: in + out,
		},
	}
}

func mkTrace(steps []workflow.Step) *workflow.Trace {
	t := &workflow.Trace{
		WorkflowID: "wf",
		StartedAt:  time.Now(),
		Steps:      steps,
		StepCount:  len(steps),
		Models:     map[string]int{},
		Agents:     map[string]int{},
	}
	for i, s := range steps {
		t.TotalInputTokens += s.Prompt.InputTokens
		t.TotalOutputTokens += s.Prompt.OutputTokens
		t.TotalTotalTokens += s.Prompt.TotalTokens
		if s.Prompt.InputTokens > t.MaxContextSize {
			t.MaxContextSize = s.Prompt.InputTokens
		}
		if i > 0 {
			delta := s.Prompt.InputTokens - steps[i-1].Prompt.InputTokens
			if delta > 0 {
				t.ContextGrowthTotal += delta
			}
		}
		if s.Prompt.RequestModel != "" {
			t.Models[s.Prompt.RequestModel]++
		}
		if s.Prompt.AgentID != "" {
			t.Agents[s.Prompt.AgentID]++
		}
	}
	return t
}

func TestEvaluateNilOrEmptyZero(t *testing.T) {
	if got := Evaluate(nil, Config{}, Weights{}); got.Total != 0 {
		t.Errorf("nil trace: %+v", got)
	}
	if got := Evaluate(&workflow.Trace{}, Config{}, Weights{}); got.Total != 0 {
		t.Errorf("empty trace: %+v", got)
	}
}

func TestHealthyTraceScoresHigh(t *testing.T) {
	steps := []workflow.Step{
		mkStep("agent-A", "gpt-4o", 200, 80),
		mkStep("agent-B", "gpt-4o-mini", 250, 100),
		mkStep("agent-A", "gpt-4o", 220, 90),
		mkStep("agent-C", "gpt-4o-mini", 200, 70),
	}
	got := Evaluate(mkTrace(steps), Config{}, Weights{})
	if got.Grade < 70 {
		t.Errorf("healthy trace grade too low: %+v", got)
	}
}

func TestOversizedContextScoresLow(t *testing.T) {
	steps := []workflow.Step{
		mkStep("a", "gpt-4o", 50_000, 100),
		mkStep("a", "gpt-4o", 80_000, 100),
	}
	got := Evaluate(mkTrace(steps), Config{}, Weights{})
	if got.Context > 0.5 {
		t.Errorf("oversized context should score < 0.5, got %f", got.Context)
	}
}

func TestRunawayLoopHurtsScore(t *testing.T) {
	steps := make([]workflow.Step, 10)
	for i := range steps {
		steps[i] = mkStep("solo-agent", "gpt-4o", 100, 50)
	}
	got := Evaluate(mkTrace(steps), Config{MaxAgentLoopRun: 4}, Weights{})
	if got.Loops > 0.4 {
		t.Errorf("solo loop should hurt loop score: %f", got.Loops)
	}
}

func TestSoloShortRunStillFine(t *testing.T) {
	steps := []workflow.Step{
		mkStep("solo", "gpt-4o", 100, 40),
		mkStep("solo", "gpt-4o", 110, 45),
	}
	got := Evaluate(mkTrace(steps), Config{}, Weights{})
	if got.Diversity < 1 {
		t.Errorf("short solo run should still hit max diversity: %f", got.Diversity)
	}
}

func TestZeroTokensProducesZeroIO(t *testing.T) {
	steps := []workflow.Step{mkStep("a", "gpt-4o", 0, 0)}
	got := Evaluate(mkTrace(steps), Config{}, Weights{})
	if got.IORatio != 0 {
		t.Errorf("zero tokens IORatio: %f", got.IORatio)
	}
}

func TestWeightsNormalisation(t *testing.T) {
	w := Weights{Context: 2, IORatio: 2, Loops: 0, Diversity: 0}.normalised()
	if w.Context != 0.5 || w.IORatio != 0.5 {
		t.Errorf("normalisation: %+v", w)
	}
	w2 := Weights{}.normalised()
	dw := DefaultWeights()
	if w2 != dw {
		t.Errorf("zero weights should default: got %+v want %+v", w2, dw)
	}
}

func TestGradeRoundedToInt(t *testing.T) {
	steps := []workflow.Step{
		mkStep("a", "gpt-4o", 100, 30),
		mkStep("b", "gpt-4o-mini", 100, 30),
		mkStep("a", "gpt-4o", 100, 30),
		mkStep("c", "gpt-4o-mini", 100, 30),
	}
	got := Evaluate(mkTrace(steps), Config{}, Weights{})
	if got.Grade < 0 || got.Grade > 100 {
		t.Errorf("grade out of [0,100]: %d", got.Grade)
	}
	approx := int(got.Total*100 + 0.5)
	if absInt(got.Grade-approx) > 1 {
		t.Errorf("grade should match Total*100 rounded: %d vs %d", got.Grade, approx)
	}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
