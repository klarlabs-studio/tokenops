// Package claudesettings reads, from Claude Code's settings files, which
// endpoint Claude Code sends requests to (ADR 0009).
//
// Claude Code's transcripts record the model that answered, not where the
// request went, so a gateway in front of it (Fireworks through FireConnect,
// OpenRouter, a company proxy) is only visible in its settings. Only the
// setting that names the endpoint is read. Keys, apiKeyHelper output and
// every other setting are left alone.
package claudesettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// BaseURL is the ANTHROPIC_BASE_URL Claude Code is configured with, or ""
// for Anthropic's default. Managed settings win over the user's, as they
// do in Claude Code. The process environment is not read: the daemon does
// not share the shell Claude Code was started from.
func BaseURL() string {
	for _, path := range settingsFiles() {
		if v := baseURLIn(path); v != "" {
			return v
		}
	}
	return ""
}

// settingsFiles lists the files to read, highest precedence first.
func settingsFiles() []string {
	var out []string
	switch runtime.GOOS {
	case "darwin":
		out = append(out, "/Library/Application Support/ClaudeCode/managed-settings.json")
	case "linux":
		out = append(out, "/etc/claude-code/managed-settings.json")
	}
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return out
		}
		dir = filepath.Join(home, ".claude")
	}
	return append(out, filepath.Join(dir, "settings.local.json"), filepath.Join(dir, "settings.json"))
}

// baseURLIn reads env.ANTHROPIC_BASE_URL from one settings file. A
// missing or unreadable file yields "".
func baseURLIn(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // fixed Claude Code settings paths
	if err != nil {
		return ""
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	return s.Env["ANTHROPIC_BASE_URL"]
}
