package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// mintDashTokenCLI must return at least 40 base64 chars (32 bytes
// encoded raw URL ~= 43 chars) and produce distinct values across
// calls — otherwise a rotate is a no-op.
func TestMintDashTokenCLI(t *testing.T) {
	a, err := mintDashTokenCLI()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 40 {
		t.Errorf("token too short (%d chars); want >=40", len(a))
	}
	b, err := mintDashTokenCLI()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("consecutive mints returned the same value: %q", a)
	}
}

// dashTokenPathCLI must honour XDG_DATA_HOME so the daemon writer
// and the CLI rotator always agree on location.
func TestDashTokenPathCLIHonoursXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	p, err := dashTokenPathCLI()
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join("tokenops", "dashboard.token")
	if !strings.HasSuffix(p, wantSuffix) {
		t.Errorf("path %q missing %s suffix", p, wantSuffix)
	}
	if !strings.HasPrefix(p, xdg) {
		t.Errorf("path %q should start with XDG_DATA_HOME (%s)", p, xdg)
	}
}
