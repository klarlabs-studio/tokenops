package sourceprobe

import (
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/infra/claudehome"
)

// PresentLocalReaders lists the local transcript readers whose data is on
// this machine, by their source names. `tokenops init` and the daemon's
// periodic check both turn these on unless the operator switched them off,
// so the two must agree on what "present" means.
func PresentLocalReaders() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = claudehome.Dir()
	}
	codexDir := os.Getenv("CODEX_HOME")
	if codexDir == "" {
		codexDir = filepath.Join(home, ".codex")
	}
	geminiDir := filepath.Join(home, ".gemini")
	if h := os.Getenv("GEMINI_CLI_HOME"); h != "" {
		geminiDir = filepath.Join(h, ".gemini")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	var out []string
	for _, c := range []struct{ name, path string }{
		{"claude-code-jsonl", filepath.Join(claudeDir, "projects")},
		{"codex-jsonl", filepath.Join(codexDir, "sessions")},
		{"opencode", filepath.Join(dataHome, "opencode", "opencode.db")},
		{"gemini-cli", filepath.Join(geminiDir, "tmp")},
		{"pi-sessions", filepath.Join(home, ".pi", "agent", "sessions")},
		{"pi-sessions", filepath.Join(home, ".omp", "agent", "sessions")},
	} {
		if len(out) > 0 && out[len(out)-1] == c.name {
			continue
		}
		if _, err := os.Stat(c.path); err == nil {
			out = append(out, c.name)
		}
	}
	return out
}
