package bootstrap

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/harnesskeys"
)

// Each key goes only to the vendor it is sent to; an unknown host or a
// mainland-China platform's key is not used.
func TestCredentialsForNamesTheEndpoint(t *testing.T) {
	got := credentialsFor([]harnesskeys.Credential{
		{BaseURL: "https://openrouter.ai/api", Key: "a"},
		{ProviderID: "openrouter", Key: "b"},
		{ProviderID: "deepseek", Key: "c"},
		{ProviderID: "moonshotai", Key: "d"},
		{ProviderID: "moonshotai-cn", Key: "e"},
		{BaseURL: "https://llm.internal.example/v1", Key: "f"},
		{ProviderID: "fireworks-ai", Key: "g"},
		{BaseURL: "https://api.portkey.ai/v1", Key: "h"},
		{Endpoint: "codebuff", Key: "i"},
		{Provider: "openai", Key: "k"},
		{Gateway: "sub2api", BaseURL: "https://s2.example", Key: "j"},
	})
	// An unknown host is a possible gateway, read only at its own address;
	// Portkey cannot read its own spend, so it is not asked. An admin key
	// goes to the admin reader only, a named gateway's to that gateway.
	want := map[string]string{"a": "openrouter", "b": "openrouter", "c": "deepseek", "d": "moonshot", "f": "gateway", "g": "fireworks",
		"i": "codebuff", "k": "openai-admin", "j": "gateway"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if want[c.Key] != c.Endpoint {
			t.Errorf("key %s → %q, want %q", c.Key, c.Endpoint, want[c.Key])
		}
		if c.Key == "j" && (c.Gateway != "sub2api" || c.BaseURL != "https://s2.example") {
			t.Errorf("a named gateway lost its name or address: %+v", c)
		}
		if c.Endpoint == "gateway" && c.Key == "f" && c.BaseURL != "https://llm.internal.example/v1" {
			t.Errorf("gateway key without its address: %+v", c)
		}
	}
}
