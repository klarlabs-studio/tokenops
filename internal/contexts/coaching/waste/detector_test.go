package waste

import (
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func mkStep(idx int, agent, hash string, inputTokens int64, ctxDelta int64) workflow.Step {
	return workflow.Step{
		Index: idx,
		Envelope: &eventschema.Envelope{
			ID: "e", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: time.Now().UTC(),
			Payload: &eventschema.PromptEvent{
				PromptHash: hash, Provider: eventschema.ProviderOpenAI,
				RequestModel: "gpt-4o", InputTokens: inputTokens,
				AgentID: agent, WorkflowID: "wf-A",
			},
		},
		Prompt: &eventschema.PromptEvent{
			PromptHash: hash, Provider: eventschema.ProviderOpenAI,
			RequestModel: "gpt-4o", InputTokens: inputTokens,
			AgentID: agent, WorkflowID: "wf-A",
		},
		ContextDelta: ctxDelta,
	}
}

func mkTrace(steps []workflow.Step) *workflow.Trace {
	t := &workflow.Trace{WorkflowID: "wf-A", Steps: steps, StepCount: len(steps)}
	for _, s := range steps {
		if s.Prompt.InputTokens > t.MaxContextSize {
			t.MaxContextSize = s.Prompt.InputTokens
		}
		if s.ContextDelta > 0 {
			t.ContextGrowthTotal += s.ContextDelta
		}
	}
	return t
}

func TestDetectsOversizedContext(t *testing.T) {
	d := New(Config{MaxContextTokens: 1000})
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-1", "h1", 500, 500),
		mkStep(1, "agent-1", "h2", 1500, 1000),
	})
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Kind == eventschema.CoachingKindTrimContext && ev.Summary == "Oversized context window" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected oversized context coaching, got %+v", events)
	}
}

func TestDetectsRunawayGrowth(t *testing.T) {
	d := New(Config{ContextGrowthLimitTokens: 100, MaxContextTokens: 1_000_000})
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-1", "h1", 50, 50),
		mkStep(1, "agent-1", "h2", 150, 100),
		mkStep(2, "agent-1", "h3", 350, 200),
	})
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Summary == "Runaway context growth" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected runaway growth coaching, got %+v", events)
	}
}

func TestDetectsAgentLoop(t *testing.T) {
	d := New(Config{MaxConsecutiveAgentLoops: 3, MaxContextTokens: 1_000_000, ContextGrowthLimitTokens: 1_000_000})
	// A planner hands off to agent-A, which then runs five times in a row
	// while nothing else in the workflow gets a turn.
	steps := []workflow.Step{mkStep(0, "planner", "h-p", 10, 0)}
	for i := 1; i <= 5; i++ {
		steps = append(steps, mkStep(i, "agent-A", fmt.Sprintf("h-%d", i), 10, 0))
	}
	trace := mkTrace(steps)
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Kind == eventschema.CoachingKindBreakRecursion {
			found = true
			if ev.AgentID != "agent-A" {
				t.Errorf("agent attribution wrong: %s", ev.AgentID)
			}
		}
	}
	if !found {
		t.Errorf("expected agent loop coaching, got %+v", events)
	}
}

func TestDetectsRecursion(t *testing.T) {
	d := New(Config{MaxContextTokens: 1_000_000, ContextGrowthLimitTokens: 1_000_000, MaxConsecutiveAgentLoops: 100})
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-A", "h-1", 10, 0),
		mkStep(1, "agent-A", "h-1", 10, 0), // duplicate hash
	})
	events := d.Detect(trace)
	found := false
	for _, ev := range events {
		if ev.Kind == eventschema.CoachingKindReuseCache {
			found = true
			if ev.ReplayMetadata["prompt_hash"] != "h-1" {
				t.Errorf("prompt_hash metadata: %s", ev.ReplayMetadata["prompt_hash"])
			}
		}
	}
	if !found {
		t.Errorf("expected recursion coaching, got %+v", events)
	}
}

func TestNilOrEmptyTraceNoOp(t *testing.T) {
	d := New(Config{})
	if events := d.Detect(nil); events != nil {
		t.Errorf("nil trace: %+v", events)
	}
	if events := d.Detect(&workflow.Trace{}); events != nil {
		t.Errorf("empty trace: %+v", events)
	}
}

func TestHealthyTraceProducesNoFindings(t *testing.T) {
	d := New(Config{}) // defaults — high thresholds
	trace := mkTrace([]workflow.Step{
		mkStep(0, "agent-A", "h-1", 100, 0),
		mkStep(1, "agent-B", "h-2", 150, 50),
		mkStep(2, "agent-A", "h-3", 200, 50),
	})
	if events := d.Detect(trace); len(events) != 0 {
		t.Errorf("healthy trace produced findings: %+v", events)
	}
}

func TestDefaultsApplied(t *testing.T) {
	d := New(Config{})
	if d.cfg.MaxContextTokens != 32_768 {
		t.Errorf("default max context = %d", d.cfg.MaxContextTokens)
	}
	if d.cfg.ContextGrowthPerStepTokens != 8_192 || d.cfg.ContextGrowthLimitTokens != 0 {
		t.Errorf("default growth = %d/step, %d total; want 8192/step and no total",
			d.cfg.ContextGrowthPerStepTokens, d.cfg.ContextGrowthLimitTokens)
	}
	if d.cfg.MaxConsecutiveAgentLoops != 4 {
		t.Errorf("default loops = %d", d.cfg.MaxConsecutiveAgentLoops)
	}
}

// Operator-configured context limits (coaching.context_limits) must win
// over the built-in workflow profiles — they exist to override them.
func TestOperatorProfileOverridesBuiltIn(t *testing.T) {
	d := New(Config{Profiles: []Profile{{
		WorkflowPrefix:   "claude-code:",
		MaxContextTokens: 100_000,
	}}})
	steps := []workflow.Step{
		mkStep(0, "agent-1", "h1", 50_000, 0),
		mkStep(1, "agent-1", "h2", 200_000, 0),
	}
	trace := mkTrace(steps)
	trace.WorkflowID = "claude-code:sess-1"
	// 200k peak: under the built-in 900k cap, over the operator's 100k.
	var found bool
	for _, ev := range d.Detect(trace) {
		if ev.Summary == "Oversized context window" {
			found = true
		}
	}
	if !found {
		t.Error("operator 100k limit ignored; built-in 900k profile still applied")
	}
}

// Without an operator profile, the built-in claude-code profile still
// applies (no regression).
func TestBuiltInProfileStillAppliesWithoutOperatorOverride(t *testing.T) {
	d := New(Config{})
	steps := []workflow.Step{mkStep(0, "agent-1", "h1", 200_000, 0)}
	trace := mkTrace(steps)
	trace.WorkflowID = "claude-code:sess-1"
	for _, ev := range d.Detect(trace) {
		if ev.Summary == "Oversized context window" {
			t.Errorf("200k context flagged despite built-in 900k claude-code cap: %+v", ev)
		}
	}
}

// Profiles with a non-matching prefix must not affect other workflows.
func TestOperatorProfileScopedToPrefix(t *testing.T) {
	d := New(Config{Profiles: []Profile{{
		WorkflowPrefix:   "codex:",
		MaxContextTokens: 1,
	}}})
	steps := []workflow.Step{mkStep(0, "agent-1", "h1", 1000, 0)}
	trace := mkTrace(steps) // WorkflowID "wf-A"
	for _, ev := range d.Detect(trace) {
		if ev.Summary == "Oversized context window" {
			t.Errorf("codex: profile leaked onto wf-A: %+v", ev)
		}
	}
}

// In a single-agent session every turn belongs to the same agent, so a
// consecutive-run count is just the session length. 3,807 turns of one
// Codex session were reported as "likely an unbounded retry or re-plan
// loop" on every real session measured. The rule needs a second agent
// to mean anything; single-agent repetition is the reuse_cache rule.
func TestSingleAgentSessionIsNotAnAgentLoop(t *testing.T) {
	d := New(Config{MaxConsecutiveAgentLoops: 3, MaxContextTokens: 1_000_000, ContextGrowthLimitTokens: 1_000_000})
	steps := []workflow.Step{}
	for i := 0; i < 50; i++ {
		steps = append(steps, mkStep(i, "codex", fmt.Sprintf("h-%d", i), 10, 0))
	}
	for _, ev := range d.Detect(mkTrace(steps)) {
		if ev.Kind == eventschema.CoachingKindBreakRecursion {
			t.Fatalf("single-agent session flagged as an agent loop: %+v", ev)
		}
	}
}

// Transcript sessions grow a steady amount per step and compact now and
// then, so their total growth scales with length. A long session at an
// ordinary per-step rate must not be called runaway growth; a session
// whose every step appends far more than usual must be.
func TestTranscriptGrowthJudgedPerStep(t *testing.T) {
	d := New(Config{})
	long := []workflow.Step{}
	for i := 0; i < 3000; i++ {
		long = append(long, mkStep(i, "codex", fmt.Sprintf("h-%d", i), 100_000, 4_000))
	}
	trace := mkTrace(long)
	trace.WorkflowID = "codex:long-ordinary"
	for _, ev := range d.Detect(trace) {
		if ev.Summary == "Runaway context growth" {
			t.Fatalf("long session at an ordinary 4k/step flagged: %+v", ev)
		}
	}

	heavy := []workflow.Step{}
	for i := 0; i < 20; i++ {
		heavy = append(heavy, mkStep(i, "codex", fmt.Sprintf("h-%d", i), 100_000, 40_000))
	}
	trace = mkTrace(heavy)
	trace.WorkflowID = "codex:heavy"
	found := false
	for _, ev := range d.Detect(trace) {
		if ev.Summary == "Runaway context growth" {
			found = true
		}
	}
	if !found {
		t.Fatal("40k/step growth not flagged")
	}
}

// A per-step rate means nothing over a handful of steps.
func TestPerStepGrowthNeedsEnoughSteps(t *testing.T) {
	d := New(Config{})
	steps := []workflow.Step{mkStep(0, "codex", "h0", 50_000, 50_000), mkStep(1, "codex", "h1", 150_000, 100_000)}
	trace := mkTrace(steps)
	trace.WorkflowID = "codex:short"
	for _, ev := range d.Detect(trace) {
		if ev.Summary == "Runaway context growth" {
			t.Fatalf("two-step session judged on a per-step rate: %+v", ev)
		}
	}
}

// An operator's per-step limit applies through coaching.context_limits.
func TestOperatorPerStepGrowthLimit(t *testing.T) {
	d := New(Config{Profiles: []Profile{{WorkflowPrefix: "claude-code:", ContextGrowthPerStepTokens: 1_000}}})
	steps := []workflow.Step{}
	for i := 0; i < 20; i++ {
		steps = append(steps, mkStep(i, "claude-code", fmt.Sprintf("h-%d", i), 10_000, 2_000))
	}
	trace := mkTrace(steps)
	trace.WorkflowID = "claude-code:p:s"
	found := false
	for _, ev := range d.Detect(trace) {
		if ev.Summary == "Runaway context growth" {
			found = true
		}
	}
	if !found {
		t.Fatal("operator per-step limit of 1k did not flag 2k/step")
	}
}

// A 25-step workflow at ~700 tokens per step crossed the old 16k total
// default. Without a profile the default is now per step.
func TestGenericDefaultGrowthIsPerStep(t *testing.T) {
	d := New(Config{})
	steps := []workflow.Step{}
	for i := 0; i < 25; i++ {
		steps = append(steps, mkStep(i, "agent-A", fmt.Sprintf("h-%d", i), 2_000, 700))
	}
	for _, ev := range d.Detect(mkTrace(steps)) {
		if ev.Summary == "Runaway context growth" {
			t.Fatalf("ordinary 700/step workflow flagged by the generic default: %+v", ev)
		}
	}
}

// An operator profile that names only a total keeps the total rule even
// though the generic default is per step.
func TestOperatorTotalOverridesPerStepDefault(t *testing.T) {
	d := New(Config{Profiles: []Profile{{WorkflowPrefix: "wf-", ContextGrowthLimitTokens: 1_000}}})
	steps := []workflow.Step{}
	for i := 0; i < 20; i++ {
		steps = append(steps, mkStep(i, "agent-A", fmt.Sprintf("h-%d", i), 5_000, 100))
	}
	found := false
	for _, ev := range d.Detect(mkTrace(steps)) {
		if ev.Summary == "Runaway context growth" {
			found = true
		}
	}
	if !found {
		t.Fatal("operator total limit of 1k did not flag 1.9k total growth")
	}
}
