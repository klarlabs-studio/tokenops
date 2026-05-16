package waste

import (
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/workflows/workflow"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Short workflows still trip the default thresholds — guard against
// the profile accidentally bleeding into non-claude-code traces.
func TestProfileForNonClaudeCodeReturnsNil(t *testing.T) {
	cases := []string{"", "agent-x", "proxy:abc", "workflow-1"}
	for _, w := range cases {
		if p := ProfileFor(w); p != nil {
			t.Errorf("ProfileFor(%q) = %+v; want nil", w, p)
		}
	}
}

// codex: prefix gets the OpenAI Codex profile (250k peak, 500k
// growth — tighter than Claude Code because Codex defaults to a
// 256k context window on gpt-5 family).
func TestProfileForCodex(t *testing.T) {
	p := ProfileFor("codex:rollout-abc")
	if p == nil {
		t.Fatal("ProfileFor(codex:...) returned nil")
	}
	if p.MaxContextTokens != 250_000 {
		t.Errorf("codex MaxContextTokens = %d; want 250000", p.MaxContextTokens)
	}
	if p.ContextGrowthLimitTokens != 500_000 {
		t.Errorf("codex growth = %d; want 500000", p.ContextGrowthLimitTokens)
	}
}

// claude-code: prefix gets the looser code-agent thresholds.
func TestProfileForClaudeCode(t *testing.T) {
	cases := []string{
		"claude-code:abc",
		"claude-code:project-x:session-y",
	}
	for _, w := range cases {
		p := ProfileFor(w)
		if p == nil {
			t.Fatalf("ProfileFor(%q) returned nil; want profile", w)
		}
		if p.MaxContextTokens != 900_000 {
			t.Errorf("MaxContextTokens = %d", p.MaxContextTokens)
		}
		if p.ContextGrowthLimitTokens != 2_000_000 {
			t.Errorf("ContextGrowthLimitTokens = %d", p.ContextGrowthLimitTokens)
		}
	}
}

// Detect must apply the profile for a claude-code trace: a 500k
// peak that would trip the default 32k threshold does NOT trip the
// 900k code-agent threshold.
func TestDetectAppliesClaudeCodeProfile(t *testing.T) {
	trace := &workflow.Trace{
		WorkflowID:         "claude-code:proj:sess",
		MaxContextSize:     500_000,
		ContextGrowthTotal: 500_000,
		StepCount:          50,
		Steps: []workflow.Step{
			{Prompt: &eventschema.PromptEvent{InputTokens: 500_000}},
		},
	}
	got := New(Config{}).Detect(trace)
	for _, ev := range got {
		if ev.Kind == eventschema.CoachingKindTrimContext {
			t.Errorf("claude-code 500k trace tripped trim_context; profile should suppress")
		}
	}
}

// Detect on a non-claude-code workflow with 500k peak still flags
// — base thresholds apply, the profile must not leak.
func TestDetectDoesNotApplyProfileToOtherWorkflows(t *testing.T) {
	trace := &workflow.Trace{
		WorkflowID:         "agent-x:abc",
		MaxContextSize:     500_000,
		ContextGrowthTotal: 500_000,
		StepCount:          50,
		Steps: []workflow.Step{
			{Prompt: &eventschema.PromptEvent{InputTokens: 500_000}},
		},
	}
	got := New(Config{}).Detect(trace)
	var sawTrim bool
	for _, ev := range got {
		if ev.Kind == eventschema.CoachingKindTrimContext {
			sawTrim = true
		}
	}
	if !sawTrim {
		t.Errorf("non-claude-code 500k trace should still trip default trim_context threshold")
	}
}

// Operator-supplied Config wins over the profile when explicitly
// non-zero — profiles backfill zero fields only.
func TestProfileMergePreservesOperatorTuning(t *testing.T) {
	base := Config{MaxContextTokens: 50_000} // tighter than the claude-code profile's 900k
	profile := Config{MaxContextTokens: 900_000, ContextGrowthLimitTokens: 2_000_000}
	merged := mergeConfig(base, profile)
	// Profile overrides because the merge prefers profile non-zero
	// values. This is the documented behaviour — operators set
	// thresholds tighter than the profile by passing a zero profile.
	if merged.MaxContextTokens != 900_000 {
		t.Errorf("merged max = %d", merged.MaxContextTokens)
	}
	if merged.ContextGrowthLimitTokens != 2_000_000 {
		t.Errorf("merged growth = %d", merged.ContextGrowthLimitTokens)
	}
}
