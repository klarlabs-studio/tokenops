package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readURLHint returns a typed error when the hint is missing so the
// MCP tool can branch on "daemon not running" cleanly. It must parse
// a well-formed payload otherwise.
func TestReadURLHint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	// Missing file: must return os.ErrNotExist (callers branch on it).
	if _, err := readURLHint(); !os.IsNotExist(err) {
		t.Fatalf("missing hint: want os.ErrNotExist, got %v", err)
	}

	// Well-formed file: parse all fields.
	payload := urlHintPayload{
		URL:       "http://127.0.0.1:8080",
		Addr:      "127.0.0.1:8080",
		TLS:       false,
		PID:       4242,
		StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	dst := filepath.Join(dir, "tokenops")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "daemon.url"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readURLHint()
	if err != nil {
		t.Fatalf("readURLHint: %v", err)
	}
	if got.URL != payload.URL || got.PID != payload.PID {
		t.Errorf("payload mismatch: got %+v want %+v", got, payload)
	}
}

// urlHintPath should land under tokenops/daemon.url regardless of
// which env var drives it, so the daemon writer and MCP reader
// always agree on location.
func TestURLHintPathHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	p, err := urlHintPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join("tokenops", "daemon.url")) {
		t.Errorf("path missing tokenops/daemon.url suffix: %q", p)
	}
	if !strings.HasPrefix(p, xdg) {
		t.Errorf("path should start with XDG_DATA_HOME (%s): %q", xdg, p)
	}
}
