package cli

import (
	"os"
	"path/filepath"
)

func resolveSettingsPath(override string) string {
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude", "settings.json")
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// commandEntry builds a Claude Code command-hook entry.
func commandEntry(exe string, args []string) map[string]any {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	return map[string]any{
		"type":    "command",
		"command": exe,
		"args":    anyArgs,
		"timeout": float64(10),
	}
}
