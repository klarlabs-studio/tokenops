package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// defaultConfigPath returns the path `tokenops init` writes to so other
// mutation verbs (plan set, provider set, rules set-root) target the
// same file by default. Mirrors resolveInitConfigPath but kept package-
// private here so the init command stays self-contained.
func defaultConfigPath() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tokenops", "config.yaml"), nil
}

// readMutableConfig loads a Config from disk WITHOUT applying env
// overrides. Mutation verbs need the on-disk truth so they don't
// accidentally serialise an env-vared value back into the file.
func readMutableConfig(path string) (config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config.Config{}, fmt.Errorf("%s does not exist; run `tokenops init` first", path)
		}
		return config.Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := config.Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return config.Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// writeMutableConfig serialises cfg back to path with secure perms.
// Round-trips the Config struct so any field the user is not currently
// editing stays as the unmarshal+marshal cycle saw it. Comments and
// blank lines from a hand-edited config are NOT preserved (acceptable
// for the v0.6+ `init`-managed config; documented in CHANGELOG).
func writeMutableConfig(path string, cfg config.Config) error {
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
