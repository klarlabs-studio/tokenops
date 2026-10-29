package cli

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/claudehome"
)

// A hook an administrator put in managed settings runs for every session, so
// hooks status must report it even though the operator's own settings.json
// does not wire it.
func TestHooksStatusReportsHooksFromManagedSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	managed := t.TempDir()
	prev := claudehome.ManagedDir
	claudehome.ManagedDir = managed
	t.Cleanup(func() { claudehome.ManagedDir = prev })
	body := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/opt/tokenops coach-hook"}]}]}}`
	if err := os.WriteFile(filepath.Join(managed, "managed-settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	userSettings := filepath.Join(t.TempDir(), "settings.json")
	st, err := hooksStatusOf(hookClientClaudeCode, userSettings, "/opt/tokenops")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Hooks) != 1 || st.Hooks[0].Hook != "coach-hook" {
		t.Errorf("hooks = %+v, want the managed coach-hook", st.Hooks)
	}
}
