package claudesettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBaseURLReadsTheUsersSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got := BaseURL(); got != "" {
		t.Fatalf("no settings: %q", got)
	}
	settings := `{"apiKeyHelper":"/x/fireconnect key export","env":{"ANTHROPIC_BASE_URL":"https://api.fireworks.ai/inference","ANTHROPIC_MODEL":"accounts/fireworks/routers/glm-fast-latest[1m]"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := BaseURL(); got != "https://api.fireworks.ai/inference" {
		t.Errorf("BaseURL() = %q", got)
	}
	// settings.local.json is read first.
	if err := os.WriteFile(filepath.Join(dir, "settings.local.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://openrouter.ai/api"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := BaseURL(); got != "https://openrouter.ai/api" {
		t.Errorf("local override: %q", got)
	}
}

// Nexus CLI sends the Fireworks key in a custom header, so the Claude
// login stays free for Anthropic turns; AUTH_TOKEN and API_KEY are unset.
func TestCredentialReadsNexusHeader(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"env":{"ANTHROPIC_BASE_URL":"https://api.fireworks.ai/inference","ANTHROPIC_CUSTOM_HEADERS":"X-Fireworks-Api-Key: fw_test\nX-Title: Claude Code\nHTTP-Referer: nexus/v0.12.0"}}`)
	if base, key := Credential(); base != "https://api.fireworks.ai/inference" || key != "fw_test" {
		t.Errorf("header: %q %q", base, key)
	}
	// The header name is matched without regard to case; AUTH_TOKEN wins.
	write(`{"env":{"ANTHROPIC_BASE_URL":"https://api.fireworks.ai/inference","ANTHROPIC_AUTH_TOKEN":"fw_token","ANTHROPIC_CUSTOM_HEADERS":"x-fireworks-api-key:fw_test"}}`)
	if _, key := Credential(); key != "fw_token" {
		t.Errorf("auth token first: %q", key)
	}
	// Other headers are never taken for a key.
	write(`{"env":{"ANTHROPIC_BASE_URL":"https://api.fireworks.ai/inference","ANTHROPIC_CUSTOM_HEADERS":"X-Title: Claude Code\nx-openai-api-key: sk-other"}}`)
	if _, key := Credential(); key != "" {
		t.Errorf("other headers: %q", key)
	}
}
