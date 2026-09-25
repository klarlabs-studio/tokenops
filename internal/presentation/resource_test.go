package presentation

import "testing"

func TestForResources(t *testing.T) {
	tests := []struct {
		name     string
		signals  []ResourceSignal
		level    string
		provider string
	}{
		{"no signals", nil, "unavailable", ""},
		{"intervention", []ResourceSignal{{Provider: "anthropic", Display: "Claude Max", Basis: "session_budget", RecommendedAction: "wait_for_reset", WindowPct: 96, Confidence: "high", SignalQualityLevel: "low", Caveat: "MCP pings only"}}, "attention", "anthropic"},
		{"spend plan risk", []ResourceSignal{{Provider: "anthropic", Display: "Claude Enterprise", Basis: "plan_headroom", OverageRisk: "medium", SignalQualityLevel: "high"}}, "attention", "anthropic"},
		{"clear but another signal unknown", []ResourceSignal{{Provider: "anthropic", Basis: "session_budget", RecommendedAction: "continue"}, {Provider: "openai", Basis: "session_budget", RecommendedAction: "unknown"}}, "uncertain", "anthropic"},
		{"low quality is not reassuring", []ResourceSignal{{Provider: "anthropic", Basis: "session_budget", RecommendedAction: "continue", SignalQualityLevel: "low"}}, "uncertain", "anthropic"},
		{"all clear", []ResourceSignal{{Provider: "anthropic", Basis: "session_budget", RecommendedAction: "continue"}}, "clear", "anthropic"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ForResources(tc.signals)
			if got.Level != tc.level || got.Provider != tc.provider {
				t.Errorf("ForResources() = %+v, want level=%q provider=%q", got, tc.level, tc.provider)
			}
		})
	}
}
