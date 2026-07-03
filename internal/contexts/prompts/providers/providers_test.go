package providers

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestAllProvidersAreUnique(t *testing.T) {
	seenID := map[eventschema.Provider]bool{}
	seenPrefix := map[string]bool{}
	for _, p := range All() {
		if seenID[p.ID] {
			t.Errorf("duplicate provider id %q", p.ID)
		}
		if seenPrefix[p.Prefix] {
			t.Errorf("duplicate prefix %q", p.Prefix)
		}
		seenID[p.ID] = true
		seenPrefix[p.Prefix] = true
		if p.Normalize == nil {
			t.Errorf("provider %s has no normalizer", p.ID)
		}
	}
}

// TestOpenAICompatibleFleetStampsOwnID guards the shared normalizer: each
// OpenAI-compatible provider must be routable AND normalize a chat request
// stamped with its OWN id (not a shared "openai"), or metering would
// misattribute every groq/deepseek/… request to OpenAI.
func TestOpenAICompatibleFleetStampsOwnID(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	for _, id := range []eventschema.Provider{
		eventschema.ProviderGroq, eventschema.ProviderDeepSeek, eventschema.ProviderXAI,
		eventschema.ProviderPerplexity, eventschema.ProviderFireworks, eventschema.ProviderCerebras,
		eventschema.ProviderTogether, eventschema.ProviderOpenRouter,
	} {
		p, ok := Lookup(id)
		if !ok {
			t.Errorf("provider %q is not routable", id)
			continue
		}
		got, err := p.Normalize("/v1/chat/completions", body)
		if err != nil {
			t.Errorf("%s normalize: %v", id, err)
			continue
		}
		if got.Provider != id {
			t.Errorf("%s normalized to provider %q; want %q", id, got.Provider, id)
		}
	}
}

// TestCohereNormalize covers Cohere's two distinct chat shapes: v2 (OpenAI-
// style messages array) and v1 (message + chat_history + preamble).
func TestCohereNormalize(t *testing.T) {
	p, ok := Lookup(eventschema.ProviderCohere)
	if !ok {
		t.Fatal("cohere should be routable")
	}

	v2 := []byte(`{"model":"command-r-plus","stream":true,"max_tokens":128,"messages":[{"role":"system","content":"x"},{"role":"user","content":"hi"}]}`)
	got, err := p.Normalize("/v2/chat", v2)
	if err != nil {
		t.Fatalf("v2 normalize: %v", err)
	}
	if got.Provider != eventschema.ProviderCohere || got.Model != "command-r-plus" ||
		!got.Stream || got.MaxOutputTokens != 128 || got.MessageCount != 2 || !got.SystemPresent {
		t.Errorf("v2 canonical wrong: %+v", got)
	}

	v1 := []byte(`{"model":"command-r","preamble":"be terse","max_tokens":64,"message":"hi","chat_history":[{"role":"USER"},{"role":"CHATBOT"}]}`)
	got, err = p.Normalize("/v1/chat", v1)
	if err != nil {
		t.Fatalf("v1 normalize: %v", err)
	}
	// 2 history turns + the current message = 3; preamble ⇒ system present.
	if got.Model != "command-r" || got.MaxOutputTokens != 64 || got.MessageCount != 3 || !got.SystemPresent {
		t.Errorf("v1 canonical wrong: %+v", got)
	}

	if _, err := p.Normalize("/v1/embed", nil); err != ErrUnknownPath {
		t.Errorf("non-chat path should be ErrUnknownPath, got %v", err)
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup(eventschema.ProviderOpenAI); !ok {
		t.Error("OpenAI should be registered")
	}
	if _, ok := Lookup(eventschema.Provider("nope")); ok {
		t.Error("unknown provider should not be found")
	}
}

func TestResolveByPath(t *testing.T) {
	cases := []struct {
		path     string
		wantID   eventschema.Provider
		wantRest string
		wantOK   bool
	}{
		{"/openai/v1/chat/completions", eventschema.ProviderOpenAI, "/v1/chat/completions", true},
		{"/anthropic/v1/messages", eventschema.ProviderAnthropic, "/v1/messages", true},
		{"/gemini/v1/models/x:generateContent", eventschema.ProviderGemini, "/v1/models/x:generateContent", true},
		{"/healthz", "", "", false},
		{"openai/foo", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			p, rest, ok := ResolveByPath(tc.path)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if p.ID != tc.wantID {
				t.Errorf("id = %q, want %q", p.ID, tc.wantID)
			}
			if rest != tc.wantRest {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

func TestParseUpstream(t *testing.T) {
	if _, err := ParseUpstream("https://api.openai.com"); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
	bads := []string{
		"",
		"not-a-url",
		"https://api.openai.com?key=1",
	}
	for _, b := range bads {
		if _, err := ParseUpstream(b); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
}

func TestNormalizeOpenAIChat(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o-mini",
		"stream":true,
		"max_tokens":256,
		"messages":[
			{"role":"system","content":"x"},
			{"role":"user","content":"hi"}
		]
	}`)
	got, err := normalizeOpenAICompatible(eventschema.ProviderOpenAI)("/v1/chat/completions", body)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Model != "gpt-4o-mini" || !got.Stream || got.MaxOutputTokens != 256 ||
		got.MessageCount != 2 || !got.SystemPresent ||
		got.Operation != "chat.completions" {
		t.Errorf("unexpected canonical: %+v", got)
	}
}

func TestNormalizeAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-7",
		"max_tokens":1024,
		"system":"helpful",
		"messages":[{"role":"user","content":"hi"}]
	}`)
	got, err := normalizeAnthropic("/v1/messages", body)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Model != "claude-3-7" || got.MaxOutputTokens != 1024 ||
		!got.SystemPresent || got.MessageCount != 1 {
		t.Errorf("unexpected canonical: %+v", got)
	}
}

func TestNormalizeGeminiGenerateContent(t *testing.T) {
	path := "/v1/models/gemini-1.5-pro:generateContent"
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hi"}]}],
		"systemInstruction":{"parts":[{"text":"x"}]},
		"generationConfig":{"maxOutputTokens":512}
	}`)
	got, err := normalizeGemini(path, body)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Model != "gemini-1.5-pro" || got.MaxOutputTokens != 512 ||
		!got.SystemPresent || got.MessageCount != 1 || got.Stream {
		t.Errorf("unexpected canonical: %+v", got)
	}
	streamPath := "/v1/models/gemini-1.5-pro:streamGenerateContent"
	got, err = normalizeGemini(streamPath, body)
	if err != nil {
		t.Fatalf("normalize stream: %v", err)
	}
	if !got.Stream {
		t.Errorf("expected Stream=true for streamGenerateContent")
	}
}

func TestNormalizeReturnsErrUnknownPath(t *testing.T) {
	if _, err := normalizeOpenAICompatible(eventschema.ProviderOpenAI)("/v1/models", nil); err != ErrUnknownPath {
		t.Errorf("openai unknown path err = %v", err)
	}
	if _, err := normalizeAnthropic("/v1/foo", nil); err != ErrUnknownPath {
		t.Errorf("anthropic unknown path err = %v", err)
	}
	if _, err := normalizeGemini("/v1/foo", nil); err != ErrUnknownPath {
		t.Errorf("gemini unknown path err = %v", err)
	}
}

func TestMistralNormalizeChat(t *testing.T) {
	p, ok := Lookup(eventschema.ProviderMistral)
	if !ok {
		t.Fatal("mistral provider not registered")
	}
	body := []byte(`{"model":"mistral-large-2511","stream":true,"max_tokens":512,
		"messages":[{"role":"system","content":"x"},{"role":"user","content":"y"}]}`)
	c, err := p.Normalize("/v1/chat/completions", body)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if c.Model != "mistral-large-2511" || !c.Stream || c.MaxOutputTokens != 512 ||
		c.MessageCount != 2 || !c.SystemPresent {
		t.Errorf("canonical = %+v", c)
	}
}

func TestMistralNormalizeFIM(t *testing.T) {
	p, _ := Lookup(eventschema.ProviderMistral)
	c, err := p.Normalize("/v1/fim/completions", []byte(`{"model":"codestral-2508","max_tokens":64}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if c.Model != "codestral-2508" || c.Operation != "fim.completions" {
		t.Errorf("canonical = %+v", c)
	}
	if _, err := p.Normalize("/v1/models", []byte(`{}`)); err == nil {
		t.Error("non-inference path should return ErrUnknownPath")
	}
}
