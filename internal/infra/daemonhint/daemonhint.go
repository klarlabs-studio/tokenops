// Package daemonhint reads the small JSON file the ingestion daemon writes
// when it binds — $XDG_DATA_HOME/tokenops/daemon.url, falling back to
// ~/.tokenops/daemon.url.
//
// It exists so every local client resolves the running daemon the same way.
// The CLI and the MCP server both need the daemon's address, and both need
// the dashboard token to call the daemon's credentialed /api/* routes; a
// second hand-rolled reader is how one of them ends up parsing a field the
// daemon renamed.
//
// The file is written 0600 because DashboardToken is a secret. Nothing here
// logs or renders it — callers put it in an Authorization header and no
// further.
package daemonhint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Payload mirrors what the daemon writes. Fields it omitted stay zero:
// an older daemon wrote no LocalURL and no DashboardToken.
type Payload struct {
	URL       string    `json:"url"`
	LocalURL  string    `json:"local_url,omitempty"`
	Addr      string    `json:"addr"`
	TLS       bool      `json:"tls"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	// DashboardToken authenticates the daemon's /dashboard and /api/*
	// routes. Empty when the daemon runs without storage, which is also
	// when those routes carry nothing worth authenticating.
	DashboardToken string `json:"dashboard_token,omitempty"`
}

// PreferredURL returns the address a client should use: the mDNS hostname
// when the daemon advertised one, the loopback URL otherwise. Keeping the
// choice here means a future addition (Tailscale MagicDNS, dynamic DNS)
// plugs in without touching callers.
func (p Payload) PreferredURL() string {
	if p.LocalURL != "" {
		return p.LocalURL
	}
	return p.URL
}

// Path resolves the hint's location. It mirrors the data-dir convention the
// rest of tokenops uses, so the file lives next to events.db rather than in
// the config directory.
func Path() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "daemon.url"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "daemon.url"), nil
}

// Read loads the hint. It returns an error satisfying os.IsNotExist when no
// daemon is running, so callers can branch on that rather than on a string.
func Read() (*Payload, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var payload Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// Token returns the dashboard token for the running daemon, or "" when there
// is no daemon, no token, or no readable hint. Best-effort by design: a
// caller that cannot find a token should send the request without one and
// let the daemon answer 401, rather than refuse to try.
func Token() string {
	p, err := Read()
	if err != nil || p == nil {
		return ""
	}
	return p.DashboardToken
}
