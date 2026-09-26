package proxy

import "testing"

func TestParseResponseUsage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want responseUsage
	}{
		{"openai", `{"model":"gpt-6-luna","usage":{"prompt_tokens":15,"completion_tokens":6,"total_tokens":21}}`, responseUsage{"gpt-6-luna", 15, 6}},
		{"anthropic", `{"model":"claude-sonnet-5","usage":{"input_tokens":12,"output_tokens":4}}`, responseUsage{"claude-sonnet-5", 12, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseResponseUsage([]byte(tt.body))
			if !ok || got != tt.want {
				t.Fatalf("parseResponseUsage() = %#v, %v; want %#v, true", got, ok, tt.want)
			}
		})
	}
}

func TestParseResponseUsageRejectsMissingOrNegativeCounts(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-6-luna"}`,
		`{"usage":{"prompt_tokens":1}}`,
		`{"usage":{"prompt_tokens":-1,"completion_tokens":2}}`,
	} {
		if _, ok := parseResponseUsage([]byte(body)); ok {
			t.Fatalf("parseResponseUsage(%s) unexpectedly succeeded", body)
		}
	}
}
