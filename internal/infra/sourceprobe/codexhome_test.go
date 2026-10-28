package sourceprobe

import (
	"path/filepath"
	"testing"
	"time"
)

// CODEX_HOME moves Codex's sessions; the origin probe must follow it,
// and a compressed rollout is a rollout too.
func TestCodexProbeFollowsCodexHomeAndCompressedRollouts(t *testing.T) {
	home := isolateHome(t)
	codexHome := filepath.Join(t.TempDir(), "codex-elsewhere")
	t.Setenv("CODEX_HOME", codexHome)

	at := time.Date(2026, 9, 2, 9, 30, 0, 0, time.UTC)
	// A rollout under the default home must not answer for CODEX_HOME.
	touch(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "01", "rollout-old.jsonl"), at.Add(-24*time.Hour))
	touch(t, filepath.Join(codexHome, "sessions", "2026", "09", "02", "rollout-cold.jsonl.zst"), at)

	probe := All(cfgWith(true, false, false, ""))["codex-jsonl"]
	if probe == nil {
		t.Fatal("no codex-jsonl probe")
	}
	got, ok := probe()
	if !ok || !got.Equal(at) {
		t.Errorf("probe = %v, %v; want %v from the compressed rollout under CODEX_HOME", got, ok, at)
	}
}
