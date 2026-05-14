package daemon

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// dashTokenPath resolves the location the daemon persists its dashboard
// auth token. Lives next to events.db / daemon.url so a single
// directory carries all transient daemon state. The MCP serve process
// reads the same path to surface the token in tokenops_dashboard.
func dashTokenPath() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "dashboard.token"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "dashboard.token"), nil
}

// loadOrMintDashToken returns the persisted token if present and
// non-empty, otherwise mints a fresh 32-byte random token and persists
// it at 0600. A configured admin token (env var) takes precedence — it
// short-circuits the read so the operator's chosen secret is the only
// source of truth.
//
// Permissions are intentionally restrictive: the file is the dashboard
// password equivalent. Wider modes would leak the token to any
// other unix user on the host.
func loadOrMintDashToken(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	p, err := dashTokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err == nil && len(data) > 0 {
		return string(data), nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read dashboard token: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint dashboard token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		return "", fmt.Errorf("persist dashboard token: %w", err)
	}
	return tok, nil
}
