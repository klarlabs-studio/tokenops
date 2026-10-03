// Package claudesettings reads, from Claude Code's settings files, which
// endpoint Claude Code sends requests to (ADR 0009).
//
// Claude Code's transcripts record the model that answered, not where the
// request went, so a gateway in front of it (Fireworks through FireConnect,
// OpenRouter, a company proxy) is only visible in its settings. Only the
// setting that names the endpoint is read; the apiKeyHelper command (its
// text, not its output), so a Fireworks reader can recognise FireConnect's;
// and the key sent to a configured gateway, for that gateway's account
// reader (ADR 0009 §7). Every other setting is left alone.
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

// APIKeyHelper is the apiKeyHelper command Claude Code is configured
// with, or "". Only the command's text is read; running it is the
// caller's decision.
func APIKeyHelper() string {
	for _, path := range settingsFiles() {
		b, err := os.ReadFile(path) //nolint:gosec // fixed Claude Code settings paths
		if err != nil {
			continue
		}
		var s struct {
			APIKeyHelper string `json:"apiKeyHelper"`
		}
		if json.Unmarshal(b, &s) == nil && s.APIKeyHelper != "" {
			return s.APIKeyHelper
		}
	}
	return ""
}

// Credential is the key Claude Code sends to its configured base URL
// (env.ANTHROPIC_AUTH_TOKEN, else env.ANTHROPIC_API_KEY), from the
// settings file that sets the base URL. Both are "" when no gateway is
// configured: Claude Code's own login is never read.
func Credential() (baseURL, key string) {
	for _, path := range settingsFiles() {
		b, err := os.ReadFile(path) //nolint:gosec // fixed Claude Code settings paths
		if err != nil {
			continue
		}
		var s struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(b, &s) != nil || s.Env["ANTHROPIC_BASE_URL"] == "" {
			continue
		}
		key := s.Env["ANTHROPIC_AUTH_TOKEN"]
		if key == "" {
			key = s.Env["ANTHROPIC_API_KEY"]
		}
		return s.Env["ANTHROPIC_BASE_URL"], key
	}
	return "", ""
}
