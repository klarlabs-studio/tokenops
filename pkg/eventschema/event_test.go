package eventschema

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSchemaVersionFormat(t *testing.T) {
	if SchemaVersion == "" {
		t.Fatal("SchemaVersion must be set")
	}
}

func TestPayloadEventTypes(t *testing.T) {
	cases := []struct {
		name    string
		payload Payload
		want    EventType
	}{
		{"prompt", &PromptEvent{}, EventTypePrompt},
		{"workflow", &WorkflowEvent{}, EventTypeWorkflow},
		{"optimization", &OptimizationEvent{}, EventTypeOptimization},
		{"coaching", &CoachingEvent{}, EventTypeCoaching},
		{"rule_source", &RuleSourceEvent{}, EventTypeRuleSource},
		{"rule_analysis", &RuleAnalysisEvent{}, EventTypeRuleAnalysis},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.payload.Type(); got != tc.want {
				t.Errorf("eventType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	env := Envelope{
		ID:            "01H8XYZ",
		SchemaVersion: SchemaVersion,
		Type:          EventTypePrompt,
		Timestamp:     time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		Source:        "proxy",
		Payload: &PromptEvent{
			PromptHash:   "sha256:abc",
			Provider:     ProviderOpenAI,
			RequestModel: "gpt-4o-mini",
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
			ContextSize:  80,
			Latency:      250 * time.Millisecond,
			Streaming:    true,
			Status:       200,
			FinishReason: "stop",
		},
	}

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("expected non-empty json")
	}
}

func TestRuleEnvelopesRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	src := Envelope{
		ID:            "01HRULE-SRC",
		SchemaVersion: SchemaVersion,
		Type:          EventTypeRuleSource,
		Timestamp:     now,
		Source:        "rule-engine",
		Payload: &RuleSourceEvent{
			SourceID:    "repo/CLAUDE.md",
			Source:      RuleSourceClaudeMD,
			Scope:       RuleScopeRepo,
			Path:        "CLAUDE.md",
			Tokenizer:   "openai/cl100k_base",
			Provider:    ProviderOpenAI,
			TotalTokens: 1200,
			TotalChars:  4800,
			Hash:        "sha256:deadbeef",
			Sections: []RuleSection{
				{ID: "CLAUDE.md#testing", Anchor: "Testing", TokenCount: 200, CharCount: 800},
				{ID: "CLAUDE.md#style", Anchor: "Style", TokenCount: 150, CharCount: 600},
			},
			IngestedAt: now,
		},
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal rule source: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("rule source envelope encoded empty")
	}

	ana := Envelope{
		ID:            "01HRULE-ANA",
		SchemaVersion: SchemaVersion,
		Type:          EventTypeRuleAnalysis,
		Timestamp:     now,
		Source:        "rule-engine",
		Payload: &RuleAnalysisEvent{
			SourceID:         "repo/CLAUDE.md",
			SectionID:        "CLAUDE.md#testing",
			WindowStart:      now.Add(-24 * time.Hour),
			WindowEnd:        now,
			Exposures:        87,
			ContextTokens:    17400,
			TokensSaved:      3100,
			RetriesAvoided:   12,
			ContextReduction: 0.19,
			QualityDelta:     0.03,
			ROIScore:         0.42,
			CompressedTokens: 90,
		},
	}
	if _, err := json.Marshal(ana); err != nil {
		t.Fatalf("marshal rule analysis: %v", err)
	}
}

func TestGenAISystemMapping(t *testing.T) {
	cases := map[Provider]string{
		ProviderOpenAI:     "openai",
		ProviderAnthropic:  "anthropic",
		ProviderGemini:     "gcp.gemini",
		ProviderUnknown:    "unknown",
		Provider("custom"): "custom",
	}
	for p, want := range cases {
		if got := GenAISystem(p); got != want {
			t.Errorf("GenAISystem(%q) = %q, want %q", p, got, want)
		}
	}
}
