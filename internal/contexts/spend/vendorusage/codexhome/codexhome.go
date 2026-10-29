// Package codexhome finds Codex's home directory the way Codex does: the
// CODEX_HOME environment variable when it is set and non-empty, otherwise
// ~/.codex (codex-rs/utils/home-dir). Sessions, config.toml and hooks.json
// all live under it, so every reader resolves its paths here.
package codexhome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir returns Codex's home directory for the current user.
func Dir() (string, error) {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

// In returns Codex's home directory for a user whose home directory is
// home: CODEX_HOME when set, else home/.codex. Readers that are handed a
// home directory use it, rather than the process's own.
func In(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// SessionsDir returns the directory holding Codex's rollouts.
func SessionsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// Owns reports whether path lies under Codex's home directory, as
// resolved now, or under a directory named .codex. It tells a Codex
// transcript from a Claude Code one by where it was written.
func Owns(path string) bool {
	if strings.Contains(filepath.ToSlash(path), "/.codex/") {
		return true
	}
	dir, err := Dir()
	if err != nil {
		return false
	}
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
