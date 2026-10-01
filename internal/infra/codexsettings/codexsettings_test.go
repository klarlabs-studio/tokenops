package codexsettings

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `model = "glm-5p3"
model_provider = "fireworks"

[model_providers.fireworks]
name = "Fireworks"
base_url = "https://api.fireworks.ai/inference/v1" # FireConnect
env_key = "FIREWORKS_API_KEY"

[model_providers."zai"]
base_url = 'https://api.z.ai/api/coding/paas/v4'

[profiles.fast]
base_url = "https://not-a-provider.example"
`

func TestBaseURLs(t *testing.T) {
	got := BaseURLs(sample)
	want := map[string]string{
		"fireworks": "https://api.fireworks.ai/inference/v1",
		"zai":       "https://api.z.ai/api/coding/paas/v4",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestProviderBaseURLHonoursCodexHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ProviderBaseURL("fireworks"); got != "https://api.fireworks.ai/inference/v1" {
		t.Errorf("ProviderBaseURL = %q", got)
	}
	if got := ProviderBaseURL("openai"); got != "" {
		t.Errorf("built-in provider has no table: %q", got)
	}
}
