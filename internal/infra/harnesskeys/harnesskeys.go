// Package harnesskeys finds the API keys the operator's harnesses already
// use, each with where it is sent, so a vendor's account reader can read
// that vendor's own limit or balance (ADR 0009 §7). A key is returned with
// its base URL or provider ID and is meant for that vendor only; nothing
// here stores, logs or prints a key.
//
// Sources, in order: Claude Code's settings (the key sent to a configured
// gateway), Codex's model_providers, opencode's auth.json and config, and
// the conventional environment variables.
package harnesskeys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/infra/codexsettings"
)

// Credential is one key and where it is sent: a base URL, or an opencode
// provider ID, or a well-known environment variable's vendor.
type Credential struct {
	// Origin names where the key was found, for status; never the key.
	Origin     string
	BaseURL    string
	ProviderID string
	// Endpoint is the account-reader endpoint a key from the variable of a
	// provider opencode does not know is for.
	Endpoint string
	// Provider is set for a key found in a variable that holds one
	// source's own credential (an admin key): it goes to that provider's
	// account reader only.
	Provider string
	Key      string
}

// EnvVars maps conventional key variables to the opencode provider ID
// (models.dev's) of the vendor they belong to, from each provider
// descriptor's EnvVars.
var EnvVars = providers.EnvVars()

// OwnEnvVars maps the key variables of providers opencode does not know to
// the endpoint their account reader takes keys for.
var OwnEnvVars = providers.OwnEnvVars()

// SourceEnvVars maps the variables holding one source's own credential (an
// organisation admin key) to the provider whose source it is.
var SourceEnvVars = providers.SourceEnvVars()

// Options points the finder at its sources; zero values use the real ones.
type Options struct {
	Getenv func(string) string
	// OpencodeData is opencode's data directory (auth.json).
	OpencodeData string
	// OpencodeConfig is opencode's config directory (opencode.json[c]).
	OpencodeConfig string
	// Claude returns Claude Code's gateway base URL and key.
	Claude func() (string, string)
	// Codex returns Codex's provider tables.
	Codex func() map[string]codexsettings.Provider
}

// Find returns every key the harnesses use, in source order.
func Find(o Options) []Credential {
	o = o.withDefaults()
	var out []Credential
	add := func(c Credential) {
		c.Key = strings.TrimSpace(c.Key)
		if c.Key != "" && !strings.ContainsAny(c.Key, " \n\t") {
			out = append(out, c)
		}
	}
	if base, key := o.Claude(); base != "" {
		add(Credential{Origin: "Claude Code settings", BaseURL: base, Key: key})
	}
	for _, p := range o.Codex() {
		key := p.BearerToken
		if key == "" && p.EnvKey != "" {
			key = o.Getenv(p.EnvKey)
		}
		if p.BaseURL != "" {
			add(Credential{Origin: "Codex config", BaseURL: p.BaseURL, Key: key})
		}
	}
	for id, key := range opencodeAuth(o.OpencodeData) {
		add(Credential{Origin: "opencode auth.json", ProviderID: id, Key: key})
	}
	for _, c := range opencodeConfig(o.OpencodeConfig, o.Getenv) {
		add(c)
	}
	for name, id := range EnvVars {
		add(Credential{Origin: "$" + name, ProviderID: id, Key: o.Getenv(name)})
	}
	for name, endpoint := range OwnEnvVars {
		add(Credential{Origin: "$" + name, Endpoint: endpoint, Key: o.Getenv(name)})
	}
	for name, id := range SourceEnvVars {
		add(Credential{Origin: "$" + name, Provider: id, Key: o.Getenv(name)})
	}
	return out
}

func (o Options) withDefaults() Options {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	home, _ := os.UserHomeDir()
	if o.OpencodeData == "" {
		base := o.Getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		}
		o.OpencodeData = filepath.Join(base, "opencode")
	}
	if o.OpencodeConfig == "" {
		base := o.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		o.OpencodeConfig = filepath.Join(base, "opencode")
	}
	if o.Claude == nil {
		o.Claude = claudesettings.Credential
	}
	if o.Codex == nil {
		o.Codex = codexsettings.ReadProviders
	}
	return o
}

// opencodeAuth reads the API keys in opencode's auth.json. OAuth logins
// (type "oauth") are subscriptions, not keys, and are skipped.
func opencodeAuth(dir string) map[string]string {
	b, err := os.ReadFile(filepath.Join(dir, "auth.json")) //nolint:gosec // opencode's own data path
	if err != nil {
		return nil
	}
	var entries map[string]struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if json.Unmarshal(b, &entries) != nil {
		return nil
	}
	out := map[string]string{}
	for id, e := range entries {
		if e.Type == "api" && e.Key != "" {
			out[id] = e.Key
		}
	}
	return out
}

// opencodeConfig reads provider.<id>.options.{apiKey, baseURL} from
// opencode's config, resolving {env:NAME} and {file:path}.
func opencodeConfig(dir string, getenv func(string) string) []Credential {
	var src []byte
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil { //nolint:gosec // opencode's own config path
			src = b
			break
		}
	}
	if src == nil {
		return nil
	}
	var cfg struct {
		Provider map[string]struct {
			Options struct {
				APIKey  string `json:"apiKey"`
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
	}
	if json.Unmarshal(StripJSONC(src), &cfg) != nil {
		return nil
	}
	var out []Credential
	for id, p := range cfg.Provider {
		key := resolveRef(p.Options.APIKey, dir, getenv)
		if key == "" {
			continue
		}
		out = append(out, Credential{Origin: "opencode config", ProviderID: id, BaseURL: p.Options.BaseURL, Key: key})
	}
	return out
}

// resolveRef expands opencode's {env:NAME} and {file:path} references.
func resolveRef(v, dir string, getenv func(string) string) string {
	switch {
	case strings.HasPrefix(v, "{env:") && strings.HasSuffix(v, "}"):
		return getenv(v[5 : len(v)-1])
	case strings.HasPrefix(v, "{file:") && strings.HasSuffix(v, "}"):
		path := v[6 : len(v)-1]
		if strings.HasPrefix(path, "~/") {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, path[2:])
		} else if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		b, err := os.ReadFile(path) //nolint:gosec // a path the operator's opencode config names
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	default:
		return v
	}
}

// StripJSONC removes // and /* */ comments and trailing commas outside
// strings, so JSONC parses as JSON.
func StripJSONC(src []byte) []byte {
	return dropTrailingCommas(dropComments(src))
}

// scanStrings calls emit for each byte, saying whether it is inside a
// string literal.
func scanStrings(src []byte, emit func(i int, inString bool) int) {
	inString, escaped := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
				i = emit(i, true)
				continue
			}
			i = emit(i, true)
			continue
		}
		if c == '"' {
			inString = true
			i = emit(i, true)
			continue
		}
		i = emit(i, false)
	}
}

func dropComments(src []byte) []byte {
	out := make([]byte, 0, len(src))
	scanStrings(src, func(i int, inString bool) int {
		switch {
		case inString:
			out = append(out, src[i])
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				out = append(out, '\n')
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && (src[i] != '*' || src[i+1] != '/') {
				i++
			}
			i++
		default:
			out = append(out, src[i])
		}
		return i
	})
	return out
}

func dropTrailingCommas(src []byte) []byte {
	out := make([]byte, 0, len(src))
	scanStrings(src, func(i int, inString bool) int {
		if !inString && src[i] == ',' {
			j := i + 1
			for j < len(src) && strings.ContainsRune(" \t\r\n", rune(src[j])) {
				j++
			}
			if j < len(src) && (src[j] == '}' || src[j] == ']') {
				return i
			}
		}
		out = append(out, src[i])
		return i
	})
	return out
}
