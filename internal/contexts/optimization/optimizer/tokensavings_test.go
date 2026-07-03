package optimizer

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestMessageContentText_OpenAIAndAnthropic(t *testing.T) {
	openai := []byte(`{"messages":[{"role":"user","content":"hello world"},{"role":"assistant","content":"hi"}]}`)
	if got := MessageContentText(openai); got != "hello world\nhi\n" {
		t.Errorf("openai content = %q", got)
	}
	// Anthropic: top-level system + array-of-parts content.
	anthropic := []byte(`{"system":"be terse","messages":[{"role":"user","content":[{"type":"text","text":"part one"},{"type":"image"},{"type":"text","text":"part two"}]}]}`)
	if got := MessageContentText(anthropic); got != "be terse\npart one\npart two\n" {
		t.Errorf("anthropic content = %q", got)
	}
	if MessageContentText([]byte(`not json`)) != "" {
		t.Error("non-json body should yield empty content")
	}
}

func TestEstimateTokenSavings_TokenizesRealDelta(t *testing.T) {
	tk := tokenizer.NewRegistry()
	before := []byte(`{"messages":[{"role":"user","content":"the quick brown fox jumps over the lazy dog again and again"}]}`)
	after := []byte(`{"messages":[{"role":"user","content":"the quick brown fox"}]}`)
	got := EstimateTokenSavings(tk, eventschema.ProviderOpenAI, before, after, 40)
	if got <= 0 {
		t.Fatalf("expected positive savings from real content delta, got %d", got)
	}
	// It must reflect the actual token delta, not the old canary (which
	// returned ~fallback/4 regardless of content). tokens(before)-tokens(after)
	// for this text is clearly less than the raw fallback of 40/4=10 would be
	// only by coincidence — assert it tracks the real difference.
	full, _ := tk.CountText(eventschema.ProviderOpenAI, MessageContentText(before))
	kept, _ := tk.CountText(eventschema.ProviderOpenAI, MessageContentText(after))
	if want := int64(full - kept); got != want {
		t.Errorf("savings = %d, want tokens(before)-tokens(after) = %d", got, want)
	}
}

func TestEstimateTokenSavings_FallbackWithoutTokenizer(t *testing.T) {
	if got := EstimateTokenSavings(nil, eventschema.ProviderOpenAI, nil, nil, 40); got != 10 {
		t.Errorf("nil tokenizer should fall back to bytes/4 = 10, got %d", got)
	}
}

func TestEstimateTokenSavings_NeverNegative(t *testing.T) {
	tk := tokenizer.NewRegistry()
	// after longer than before → delta clamps to 0, not negative.
	before := []byte(`{"messages":[{"role":"user","content":"short"}]}`)
	after := []byte(`{"messages":[{"role":"user","content":"a much much longer replacement string"}]}`)
	if got := EstimateTokenSavings(tk, eventschema.ProviderOpenAI, before, after, 0); got != 0 {
		t.Errorf("negative delta must clamp to 0, got %d", got)
	}
}
