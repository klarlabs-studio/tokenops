package daemon

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/config"
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

// loadOrMintDashToken returns the persisted token if present and at
// least config.MinAdminTokenLength long, otherwise mints a fresh 32-byte
// random token and persists it at 0600. A configured admin token (env
// var or config, length-checked by config.Validate) takes precedence: it
// short-circuits the read so the operator's chosen secret is the only
// source of truth.
//
// Permissions are intentionally restrictive: the file is the dashboard
// password equivalent. The directory is made 0700 and an existing file
// is tightened to 0600 on every start, so a token left readable by an
// older build or a restore does not stay that way. A persisted token
// that is too short is replaced rather than refused: refusing would keep
// the daemon from starting over a file the operator never chose, and
// clients learn the new token from the URL hint the daemon rewrites at
// start.
func loadOrMintDashToken(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	p, err := dashTokenPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("restrict dashboard token directory: %w", err)
	}
	data, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read dashboard token: %w", err)
	}
	if err == nil && len(data) >= config.MinAdminTokenLength {
		if err := os.Chmod(p, 0o600); err != nil {
			return "", fmt.Errorf("restrict dashboard token: %w", err)
		}
		return string(data), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint dashboard token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		return "", fmt.Errorf("persist dashboard token: %w", err)
	}
	// WriteFile keeps the mode of a file that already existed.
	if err := os.Chmod(p, 0o600); err != nil {
		return "", fmt.Errorf("restrict dashboard token: %w", err)
	}
	return tok, nil
}
