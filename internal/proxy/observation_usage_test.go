package proxy

import "testing"

func TestParseResponseUsage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want responseUsage
	}{
		{"openai", `{"model":"gpt-6-luna","usage":{"prompt_tokens":15,"completion_tokens":6,"total_tokens":21}}`, responseUsage{Model: "gpt-6-luna", InputTokens: 15, OutputTokens: 6}},
		{"anthropic", `{"model":"claude-sonnet-5","usage":{"input_tokens":12,"output_tokens":4}}`, responseUsage{Model: "claude-sonnet-5", InputTokens: 12, OutputTokens: 4}},
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

func TestParseResponseUsageFromOpenAIResponsesStream(t *testing.T) {
	body := `event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","name":"shell"}}

event: response.completed
data: {"type":"response.completed","response":{"model":"gpt-6-sol-2026-09-01","status":"completed","usage":{"input_tokens":120,"output_tokens":30,"input_tokens_details":{"cached_tokens":80}},"output":[{"type":"function_call"},{"type":"message"}]}}

data: [DONE]

`
	want := responseUsage{
		Model: "gpt-6-sol-2026-09-01", InputTokens: 120, OutputTokens: 30,
		CachedInputTokens: 80, FinishReason: "completed", ToolCallCount: 1,
	}
	got, ok := parseResponseUsage([]byte(body))
	if !ok || got != want {
		t.Fatalf("parseResponseUsage() = %#v, %v; want %#v, true", got, ok, want)
	}
}

func TestParseResponseUsageFromAnthropicMessagesStream(t *testing.T) {
	body := "event: message_start\r\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-5\",\"usage\":{\"input_tokens\":90,\"cache_read_input_tokens\":60,\"cache_creation_input_tokens\":30}}}\r\n\r\n" +
		"event: content_block_start\r\n" +
		"data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\":\"Read\"}}\r\n\r\n" +
		"event: message_delta\r\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":18}}\r\n\r\n"
	// Anthropic's input_tokens excludes cache reads and writes; the event's
	// InputTokens is the whole prompt, with the cache read as its subset.
	want := responseUsage{
		Model: "claude-sonnet-5", InputTokens: 90 + 60 + 30, OutputTokens: 18,
		CachedInputTokens: 60, FinishReason: "tool_use", ToolCallCount: 1,
	}
	got, ok := parseResponseUsage([]byte(body))
	if !ok || got != want {
		t.Fatalf("parseResponseUsage() = %#v, %v; want %#v, true", got, ok, want)
	}
}

func TestParseResponseUsageFromAnthropicMessage(t *testing.T) {
	body := `{"model":"claude-sonnet-5","stop_reason":"end_turn","usage":{"input_tokens":50,"cache_read_input_tokens":100000,"cache_creation_input_tokens":2000,"output_tokens":40}}`
	got, ok := parseResponseUsage([]byte(body))
	if !ok || got.InputTokens != 102050 || got.CachedInputTokens != 100000 || got.OutputTokens != 40 {
		t.Fatalf("parseResponseUsage() = %#v, %v; want input 102050, cached 100000, output 40", got, ok)
	}
}

func TestParseResponseUsageRejectsIncompleteStream(t *testing.T) {
	body := "event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	if _, ok := parseResponseUsage([]byte(body)); ok {
		t.Fatal("incomplete stream unexpectedly reported authoritative usage")
	}
}
