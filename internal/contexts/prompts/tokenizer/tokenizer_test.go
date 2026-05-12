package tokenizer

import (
	"errors"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestHeuristicTextCount(t *testing.T) {
	tk := NewOpenAITokenizer()
	cases := []struct {
		in       string
		min, max int
	}{
		{"", 0, 0},
		{"hello", 1, 2},                     // 5 chars / 4 ≈ 1.25 → 2
		{strings.Repeat("a", 400), 95, 105}, // 400/4 = 100
	}
	for _, tc := range cases {
		got := tk.CountText(tc.in)
		if got < tc.min || got > tc.max {
			t.Errorf("CountText(%q) = %d, want in [%d, %d]", tc.in, got, tc.min, tc.max)
		}
	}
}

func TestNonASCIIDenser(t *testing.T) {
	tk := NewOpenAITokenizer()
	ascii := tk.CountText(strings.Repeat("a", 100))
	cjk := tk.CountText(strings.Repeat("漢", 100))
	if cjk <= ascii {
		t.Errorf("CJK should tokenise denser per char: ascii=%d cjk=%d", ascii, cjk)
	}
}

func TestProviderRatiosDiffer(t *testing.T) {
	const text = "The quick brown fox jumps over the lazy dog."
	got := map[eventschema.Provider]int{
		eventschema.ProviderOpenAI:    NewOpenAITokenizer().CountText(text),
		eventschema.ProviderAnthropic: NewAnthropicTokenizer().CountText(text),
		eventschema.ProviderGemini:    NewGeminiTokenizer().CountText(text),
	}
	// Anthropic ratio is 3.5 (denser), so should be >= OpenAI/Gemini.
	if got[eventschema.ProviderAnthropic] < got[eventschema.ProviderOpenAI] {
		t.Errorf("Anthropic should produce >= OpenAI for English: %v", got)
	}
}

func TestCountMessagesAddsEnvelope(t *testing.T) {
	tk := NewOpenAITokenizer()
	plain := tk.CountText("hello")
	msgs := []Message{{Role: "user", Content: "hello"}}
	wrapped := tk.CountMessages(msgs)
	if wrapped <= plain {
		t.Errorf("CountMessages should include overhead: plain=%d wrapped=%d", plain, wrapped)
	}
}

func TestRegistryLookupAndDefaults(t *testing.T) {
	r := NewRegistry()
	for _, p := range []eventschema.Provider{
		eventschema.ProviderOpenAI, eventschema.ProviderAnthropic, eventschema.ProviderGemini,
	} {
		tk, err := r.Lookup(p)
		if err != nil {
			t.Errorf("lookup %s: %v", p, err)
			continue
		}
		if tk.Provider() != p {
			t.Errorf("provider mismatch: got %s want %s", tk.Provider(), p)
		}
	}
}

func TestRegistryUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Lookup("nonsense")
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("err = %v, want ErrUnknownProvider", err)
	}
}

func TestRegistrySetOverride(t *testing.T) {
	r := NewRegistry()
	r.Set(constTokenizer{provider: eventschema.ProviderOpenAI, count: 9999})
	got, err := r.CountText(eventschema.ProviderOpenAI, "anything")
	if err != nil || got != 9999 {
		t.Errorf("override lost: got %d err %v", got, err)
	}
}

type constTokenizer struct {
	provider eventschema.Provider
	count    int
}

func (c constTokenizer) Provider() eventschema.Provider { return c.provider }
func (c constTokenizer) CountText(string) int           { return c.count }
func (c constTokenizer) CountMessages([]Message) int    { return c.count }

func TestExtractOpenAIMessages(t *testing.T) {
	body := []byte(`{
        "model": "gpt-4o-mini",
        "messages": [
            {"role": "system", "content": "You are concise."},
            {"role": "user", "content": "Hi"},
            {"role": "assistant", "content": [
                {"type": "text", "text": "Hello!"},
                {"type": "image_url", "image_url": {"url": "..."}}
            ]}
        ]
    }`)
	msgs, err := ExtractMessages(eventschema.ProviderOpenAI, body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || msgs[0].Content != "You are concise." {
		t.Errorf("system msg lost: %+v", msgs[0])
	}
	if msgs[2].Content != "Hello!" {
		t.Errorf("array content not flattened: %+v", msgs[2])
	}
}

func TestExtractAnthropicMessages(t *testing.T) {
	body := []byte(`{
        "model": "claude-sonnet-4-6",
        "system": "You are concise.",
        "messages": [
            {"role": "user", "content": "Hi"},
            {"role": "assistant", "content": [
                {"type": "text", "text": "Hello"}
            ]}
        ]
    }`)
	msgs, err := ExtractMessages(eventschema.ProviderAnthropic, body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d, want 3", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "You are concise." {
		t.Errorf("system extracted incorrectly: %+v", msgs[0])
	}
	if msgs[2].Content != "Hello" {
		t.Errorf("assistant content lost: %+v", msgs[2])
	}
}

func TestExtractGeminiMessages(t *testing.T) {
	body := []byte(`{
        "systemInstruction": {"parts": [{"text": "Be concise."}]},
        "contents": [
            {"role": "user", "parts": [{"text": "Hello"}, {"text": "world"}]},
            {"role": "model", "parts": [{"text": "Hi"}]}
        ]
    }`)
	msgs, err := ExtractMessages(eventschema.ProviderGemini, body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d, want 3", len(msgs))
	}
	if !strings.Contains(msgs[1].Content, "Hello") || !strings.Contains(msgs[1].Content, "world") {
		t.Errorf("multi-part not joined: %q", msgs[1].Content)
	}
	if msgs[0].Content != "Be concise." {
		t.Errorf("system instruction lost: %q", msgs[0].Content)
	}
}

func TestExtractEmptyBody(t *testing.T) {
	msgs, err := ExtractMessages(eventschema.ProviderOpenAI, nil)
	if err != nil || msgs != nil {
		t.Errorf("empty body: %v / %+v", err, msgs)
	}
}

func TestExtractUnknownProvider(t *testing.T) {
	_, err := ExtractMessages("something-else", []byte(`{}`))
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("err = %v", err)
	}
}

func TestPreflightCountUsesMessages(t *testing.T) {
	r := NewRegistry()
	body := []byte(`{"messages":[{"role":"user","content":"hello world"}]}`)
	got, err := r.PreflightCount(eventschema.ProviderOpenAI, body)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	tk, _ := r.Lookup(eventschema.ProviderOpenAI)
	want := tk.CountMessages([]Message{{Role: "user", Content: "hello world"}})
	if got != want {
		t.Errorf("preflight = %d, want %d (matched messages path)", got, want)
	}
}

func TestPreflightCountFallsBackToText(t *testing.T) {
	r := NewRegistry()
	// Body with no messages — preflight should fall back to raw-text count.
	body := []byte(`raw completion-style prompt`)
	got, err := r.PreflightCount(eventschema.ProviderOpenAI, body)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if got <= 0 {
		t.Errorf("expected non-zero fallback count, got %d", got)
	}
}

func TestJoinMessageContent(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	got := JoinMessageContent(msgs)
	if !strings.Contains(got, "user: hi") || !strings.Contains(got, "assistant: hello") {
		t.Errorf("joined content unexpected: %q", got)
	}
}

func TestEmptyTokenizer(t *testing.T) {
	tk := NewAnthropicTokenizer()
	if got := tk.CountText(""); got != 0 {
		t.Errorf("empty CountText = %d", got)
	}
	if got := tk.CountMessages(nil); got != 0 {
		t.Errorf("nil messages = %d", got)
	}
}
