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
