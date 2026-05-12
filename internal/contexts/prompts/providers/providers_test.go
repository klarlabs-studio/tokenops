package providers

import (
	"testing"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
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
	got, err := normalizeOpenAI("/v1/chat/completions", body)
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
	if _, err := normalizeOpenAI("/v1/models", nil); err != ErrUnknownPath {
		t.Errorf("openai unknown path err = %v", err)
	}
	if _, err := normalizeAnthropic("/v1/foo", nil); err != ErrUnknownPath {
		t.Errorf("anthropic unknown path err = %v", err)
	}
	if _, err := normalizeGemini("/v1/foo", nil); err != ErrUnknownPath {
		t.Errorf("gemini unknown path err = %v", err)
	}
}
