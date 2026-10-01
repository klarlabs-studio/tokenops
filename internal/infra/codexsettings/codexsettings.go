// Package codexsettings reads, from Codex's config.toml, the base URL of
// each model provider a session can name (ADR 0009). Each Codex rollout
// records its session's model_provider; the base URL behind that ID says
// which endpoint, and so which biller, served it. Only base_url is read:
// keys, env_key names and every other setting are left alone.
package codexsettings

import (
	"os"
	"path/filepath"
	"strings"
)

// ConfigPath is Codex's config.toml, honouring CODEX_HOME.
func ConfigPath() string {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".codex")
	}
	return filepath.Join(dir, "config.toml")
}

// ProviderBaseURL is the base_url of [model_providers.<id>] in Codex's
// config, or "" when the provider or its base URL is absent.
func ProviderBaseURL(id string) string {
	b, err := os.ReadFile(ConfigPath()) //nolint:gosec // Codex's own config path
	if err != nil {
		return ""
	}
	return BaseURLs(string(b))[id]
}

// BaseURLs reads every [model_providers.<id>] table's base_url from a
// config.toml. It understands the subset Codex writes: a table header on
// its own line and `key = "value"` pairs, with comments.
func BaseURLs(src string) map[string]string {
	out := map[string]string{}
	current := ""
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			current = ""
			header := strings.Trim(strings.TrimSpace(strings.SplitN(line, "#", 2)[0]), "[]")
			if id, ok := strings.CutPrefix(header, "model_providers."); ok {
				current = strings.Trim(id, `"'`)
			}
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "base_url" {
			continue
		}
		value = strings.TrimSpace(strings.SplitN(strings.TrimSpace(value), " #", 2)[0])
		out[current] = strings.Trim(value, `"'`)
	}
	return out
}
