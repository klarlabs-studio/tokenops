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
			path, err := writeURLHint(c.addr, c.tls, "", "")
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
			st, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat hint: %v", err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Errorf("daemon.url mode = %o; want 0600 (payload carries dashboard_token)", st.Mode().Perm())
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
	if _, err := writeURLHint("127.0.0.1:8080", false, "", ""); err != nil {
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

// A daemon shutting down must not delete a hint another daemon wrote. The
// hint vanished on the operator's machine while the daemon kept running,
// and every MCP surface then reported "no ingestion daemon is reachable"
// for a healthy one: an exiting process (a duplicate, or the old instance
// of a restart finishing after its successor booted) removed the file
// unconditionally.
func TestRemoveURLHintSparesAnotherDaemonsHint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	p := filepath.Join(dir, "tokenops", "daemon.url")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	other, _ := json.Marshal(urlHintPayload{URL: "http://127.0.0.1:7878", PID: os.Getpid() + 1})
	if err := os.WriteFile(p, other, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := removeURLHint(); err != nil {
		t.Fatalf("removeURLHint: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("another daemon's hint was deleted: %v", err)
	}
}

// Our own hint still goes, so a stale URL does not outlive the process.
func TestRemoveURLHintRemovesOwnHint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	p, err := writeURLHint("127.0.0.1:8080", false, "", "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := removeURLHint(); err != nil {
		t.Fatalf("removeURLHint: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("own hint should be gone; stat err: %v", err)
	}
}

// The hint file is 0600 because it carries the dashboard token, and the
// comment there says so. Its directory was created 0755, while
// `tokenops init` creates the storage directory beside it at 0700 — two
// paths disagreeing about the mode of the same data directory.
//
// The token is not exposed by this: the file's own mode holds. But a
// directory whose mode depends on which code path created it first is a
// permission that cannot be reasoned about, and the fix is one line.
func TestURLHintDirectoryIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	path, err := writeURLHint("127.0.0.1:7070", false, "", "tok")
	if err != nil {
		t.Fatalf("writeURLHint: %v", err)
	}
	t.Cleanup(func() { _ = removeURLHint() })

	fi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("hint directory mode = %#o, want 0700 — it holds a file "+
			"carrying the dashboard token", perm)
	}
}
