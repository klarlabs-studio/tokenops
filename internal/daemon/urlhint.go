package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

// urlHintPayload is the small JSON blob written to ~/.tokenops/daemon.url
// (or $XDG_DATA_HOME/tokenops/daemon.url) when the daemon binds its
// listener. The MCP `serve` process reads it to surface a clickable
// dashboard URL via the tokenops_dashboard tool.
//
// The schema is intentionally minimal: URL is the only field readers
// need to act on; the others are diagnostic so a stale file is easy to
// reason about. PID is recorded so a future health check can verify
// the daemon is still alive before trusting the URL.
type urlHintPayload struct {
	URL       string    `json:"url"`
	Addr      string    `json:"addr"`
	TLS       bool      `json:"tls"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// urlHintPath resolves the path under which the daemon writes its
// listen URL. The location mirrors tokenops's data-dir convention
// ($XDG_DATA_HOME/tokenops first, then ~/.tokenops) so the file lives
// next to events.db rather than in the config dir.
func urlHintPath() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "daemon.url"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "daemon.url"), nil
}

// writeURLHint records the daemon's listen URL on disk so the MCP
// server can return a clickable link. addr is the actual bound address
// (post :0 resolution); tls flips the scheme. localhost normalisation
// turns 0.0.0.0/:: into 127.0.0.1 so the URL is actually clickable
// from the operator's machine.
func writeURLHint(addr string, tls bool) (string, error) {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil && (host == "" || host == "0.0.0.0" || host == "::" || host == "[::]") {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	url := scheme + "://" + addr
	payload := urlHintPayload{
		URL:       url,
		Addr:      addr,
		TLS:       tls,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
	}
	p, err := urlHintPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	// Atomic-ish write: write to tmp + rename so a half-written file
	// is never observable by readers.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return p, nil
}

// removeURLHint deletes the URL hint file on daemon shutdown so a
// stale URL doesn't survive the process. Failures are reported to the
// caller (logged at info level) rather than swallowed.
func removeURLHint() error {
	p, err := urlHintPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
