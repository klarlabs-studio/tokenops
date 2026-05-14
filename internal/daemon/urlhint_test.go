package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeURLHint must produce a payload the MCP-side reader can parse,
// and the URL field must use a clickable host (not 0.0.0.0). Tests
// drive the path resolver via XDG_DATA_HOME so the hint lands in a
// per-test tempdir.
func TestWriteURLHintNormalizesBindAddress(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cases := []struct {
		name    string
		addr    string
		tls     bool
		wantURL string
	}{
		{"loopback", "127.0.0.1:8080", false, "http://127.0.0.1:8080"},
		{"wildcard ipv4", "0.0.0.0:9090", false, "http://127.0.0.1:9090"},
		{"wildcard ipv6", "[::]:7070", true, "https://127.0.0.1:7070"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, err := writeURLHint(c.addr, c.tls, "")
			if err != nil {
				t.Fatalf("writeURLHint: %v", err)
			}
			wantSuffix := filepath.Join("tokenops", "daemon.url")
			if !strings.HasSuffix(path, wantSuffix) {
				t.Errorf("path should end in %s; got %q", wantSuffix, path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read hint: %v", err)
			}
			var payload urlHintPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if payload.URL != c.wantURL {
				t.Errorf("URL: got %q want %q", payload.URL, c.wantURL)
			}
			if payload.PID == 0 {
				t.Errorf("PID should be non-zero")
			}
		})
	}
}

// removeURLHint must be idempotent so daemon shutdown after a failed
// write doesn't surface a misleading error.
func TestRemoveURLHintIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	// First removal on an empty dir must succeed.
	if err := removeURLHint(); err != nil {
		t.Fatalf("remove on empty dir: %v", err)
	}
	// Write a hint, then remove twice.
	if _, err := writeURLHint("127.0.0.1:8080", false, ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := removeURLHint(); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	if err := removeURLHint(); err != nil {
		t.Fatalf("second remove (idempotent): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tokenops", "daemon.url")); !os.IsNotExist(err) {
		t.Errorf("hint file should be gone; stat err: %v", err)
	}
}
