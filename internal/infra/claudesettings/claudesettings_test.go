package claudesettings

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/claudehome"
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
}

// Managed settings rank above the user's settings.json, and a drop-in
// above managed-settings.json, as Claude Code merges them.
func TestBaseURLManagedSettingsWin(t *testing.T) {
	dir := t.TempDir()
	managed := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	prev := claudehome.ManagedDir
	claudehome.ManagedDir = managed
	t.Cleanup(func() { claudehome.ManagedDir = prev })
	writeJSON := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(filepath.Join(dir, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://user.example"}}`)
	if got := BaseURL(); got != "https://user.example" {
		t.Fatalf("user only: %q", got)
	}
	writeJSON(filepath.Join(managed, "managed-settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://managed.example"}}`)
	if got := BaseURL(); got != "https://managed.example" {
		t.Errorf("managed file over user: %q", got)
	}
	writeJSON(filepath.Join(managed, "managed-settings.d", "20-gateway.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://dropin.example"}}`)
	if got := BaseURL(); got != "https://dropin.example" {
		t.Errorf("drop-in over managed file: %q", got)
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

// TestMain points the managed-settings directory at an empty fixture, so
// a machine with managed settings installed cannot change the results.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "claudesettings-managed")
	if err != nil {
		panic(err)
	}
	claudehome.ManagedDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
