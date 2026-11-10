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
//
// The config remembers the file it was read from, so that WriteMutable
// writes what the caller changed and nothing else. A config is read for
// one change: read it again to make another.
func ReadMutable(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("%s does not exist; run `tokenops init` first", path)
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := decodeMutable(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.readFrom = &readFile{path: filepath.Clean(path), data: data}
	return cfg, nil
}

// readFile is a config file as ReadMutable found it.
type readFile struct {
	path string
	data []byte
}

// decodeMutable is the config a file's content stands for, before any
// environment override.
func decodeMutable(data []byte) (Config, error) {
	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.renamePlans()
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

// WriteMutable writes cfg to path with secure perms after validating.
//
// A config that came from ReadMutable is written as a change to the file:
// the keys the caller changed since the read are set, and the rest of the
// file stays as it is on disk — what another process wrote meanwhile, a
// key this binary does not know (a newer TokenOps wrote it), comments and
// order. When the file changed in the same place, the write is refused
// with a ChangedError. A config from anywhere else, or one whose file is
// gone, is written whole.
//
// Either way the read, the merge and the write happen under one lock,
// and the file is replaced by a rename (replaceFile): the CLI, the MCP
// server and the daemon all write this file, and it holds every
// credential and every guardrail.
//
// The permissions are set on every write, not only at creation: a config
// written by an older TokenOps, or created by hand, kept its 0644 while
// every later `vendor-usage setup` wrote a fresh credential into it.
func WriteMutable(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config after mutation: %w", err)
	}
	mine, err := yaml.Marshal(cfg)
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
	return replaceFile(path, func(current []byte, exists bool) ([]byte, error) {
		if !exists || cfg.readFrom == nil || cfg.readFrom.path != filepath.Clean(path) {
			return mine, nil
		}
		return mergeChange(path, cfg.readFrom.data, mine, current)
	})
}
