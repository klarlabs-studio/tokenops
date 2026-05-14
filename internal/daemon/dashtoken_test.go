package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// loadOrMintDashToken honours an explicit configured token over the
// persisted file — operator-supplied secrets must always win.
func TestLoadOrMintDashTokenConfiguredWins(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tok, err := loadOrMintDashToken("explicit-secret")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "explicit-secret" {
		t.Errorf("configured token must be returned verbatim; got %q", tok)
	}
}

// On a fresh host (no persisted file) the function mints a random
// token, persists it at 0600, and subsequent reads return the same
// value. Permissions are critical — this file is the dashboard
// password equivalent.
func TestLoadOrMintDashTokenMintsAndPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	tok1, err := loadOrMintDashToken("")
	if err != nil {
		t.Fatal(err)
	}
	if len(tok1) < 20 {
		t.Errorf("minted token too short: %d bytes", len(tok1))
	}
	// Persisted at 0600 on POSIX. Windows applies ACLs instead of
	// the unix permission bits, so os.Stat reports the file as
	// world-readable regardless of WriteFile's mode arg — skip the
	// check there. The OS still gates access via the ACL inherited
	// from the user's profile directory.
	p, _ := dashTokenPath()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Errorf("token file mode = %v; want 0600", info.Mode().Perm())
		}
	}
	// Second call returns the same persisted value.
	tok2, err := loadOrMintDashToken("")
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != tok2 {
		t.Errorf("persisted token should round-trip; got %q vs %q", tok1, tok2)
	}
}

// dashTokenPath must land under tokenops/dashboard.token regardless
// of which env var drives it, so the daemon writer and MCP reader
// agree on location.
func TestDashTokenPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	p, err := dashTokenPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("tokenops", "dashboard.token")
	if filepath.Base(filepath.Dir(p)) != "tokenops" || filepath.Base(p) != "dashboard.token" {
		t.Errorf("path %q should end with %s", p, want)
	}
}
