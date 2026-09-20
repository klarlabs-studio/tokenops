package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultPath returns the config location `tokenops init` writes to —
// $XDG_CONFIG_HOME/tokenops/config.yaml or ~/.config/tokenops/config.yaml.
// Every mutation surface (CLI verbs, MCP config tools) targets this
// file by default so there is one on-disk truth.
func DefaultPath() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tokenops", "config.yaml"), nil
}

// ReadMutable loads a Config from disk WITHOUT applying env overrides.
// Mutation verbs need the on-disk truth so they don't accidentally
// serialise an env-vared value back into the file.
func ReadMutable(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("%s does not exist; run `tokenops init` first", path)
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// Perms the config and its directory are kept at. config.yaml holds
// every vendor credential TokenOps has been handed — the claude.ai
// session, the Cursor cookie, the Copilot OAuth token, the Anthropic
// admin key, the dashboard token — so nothing else on the machine has
// any business reading it.
const (
	ConfigFilePerm os.FileMode = 0o600
	ConfigDirPerm  os.FileMode = 0o700
)

// WriteMutable serialises cfg back to path with secure perms after
// validating. Round-trips the Config struct; comments and blank lines
// from a hand-edited config are NOT preserved (documented behaviour of
// the init-managed config).
//
// The explicit Chmod is the point, not belt-and-braces: os.WriteFile
// applies its mode only when it creates the file, and os.MkdirAll leaves
// an existing directory alone. A config written by an older TokenOps, or
// created by hand, kept its 0644 while every later `vendor-usage setup`
// wrote a fresh credential into it.
func WriteMutable(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config after mutation: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, ConfigDirPerm); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, ConfigDirPerm); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	if err := os.WriteFile(path, data, ConfigFilePerm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, ConfigFilePerm); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
