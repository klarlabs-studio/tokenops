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

// WriteMutable serialises cfg back to path with secure perms after
// validating. Round-trips the Config struct; comments and blank lines
// from a hand-edited config are NOT preserved (documented behaviour of
// the init-managed config).
func WriteMutable(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config after mutation: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
