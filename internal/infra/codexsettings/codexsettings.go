// Package codexsettings reads, from Codex's config.toml, the base URL of
// each model provider a session can name (ADR 0009). Each Codex rollout
// records its session's model_provider; the base URL behind that ID says
// which endpoint, and so which biller, served it. The key a provider is
// called with (env_key's variable, or experimental_bearer_token) is read
// for the vendor account readers (ADR 0009 §7), for that vendor only.
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

// Provider is one [model_providers.<id>] table.
type Provider struct {
	BaseURL string
	// EnvKey names the environment variable holding the key.
	EnvKey string
	// BearerToken is a key written into the config itself.
	BearerToken string
}

// BaseURLs reads every [model_providers.<id>] table's base_url from a
// config.toml.
func BaseURLs(src string) map[string]string {
	out := map[string]string{}
	for id, p := range Providers(src) {
		if p.BaseURL != "" {
			out[id] = p.BaseURL
		}
	}
	return out
}

// ReadProviders reads Codex's config and returns its provider tables; a
// missing config has none.
func ReadProviders() map[string]Provider {
	b, err := os.ReadFile(ConfigPath()) //nolint:gosec // Codex's own config path
	if err != nil {
		return nil
	}
	return Providers(string(b))
}

// Providers reads every [model_providers.<id>] table from a config.toml.
// It understands the subset Codex writes: a table header on its own line
// and `key = "value"` pairs, with comments.
func Providers(src string) map[string]Provider {
	out := map[string]Provider{}
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
				out[current] = Provider{}
			}
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(strings.SplitN(strings.TrimSpace(value), " #", 2)[0]), `"'`)
		p := out[current]
		switch strings.TrimSpace(key) {
		case "base_url":
			p.BaseURL = value
		case "env_key":
			p.EnvKey = value
		case "experimental_bearer_token":
			p.BearerToken = value
		}
		out[current] = p
	}
	return out
}

// Model is the top-level model a config.toml asks for, "" when it names
// none. Only lines before the first table are top level.
func Model(src string) string {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			return ""
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "model" {
			continue
		}
		val = strings.TrimSpace(val)
		if i := strings.Index(val, "#"); i >= 0 && !strings.HasPrefix(val, "\"") {
			val = strings.TrimSpace(val[:i])
		}
		val, _, _ = strings.Cut(strings.Trim(val, "\""), "\"")
		return strings.TrimSpace(val)
	}
	return ""
}

// ReadModel is Model of Codex's own config.toml.
func ReadModel() string {
	b, err := os.ReadFile(ConfigPath()) //nolint:gosec // Codex's own config path
	if err != nil {
		return ""
	}
	return Model(string(b))
}
