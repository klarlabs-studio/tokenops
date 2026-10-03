package daemon

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
	})
	want := map[string]string{"a": "openrouter", "b": "openrouter", "c": "deepseek", "d": "moonshot", "g": "fireworks"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if want[c.Key] != c.Endpoint {
			t.Errorf("key %s → %q, want %q", c.Key, c.Endpoint, want[c.Key])
		}
	}
}
