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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.payload.eventType(); got != tc.want {
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
