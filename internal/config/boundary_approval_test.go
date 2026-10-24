package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
)

func TestBoundaryApproval(t *testing.T) {
	for _, tc := range []struct {
		yaml string
		want presence.Mode
		err  string
	}{
		{yaml: "", want: presence.ModeChannel},
		{yaml: "boundary_approval: presence\n", want: presence.ModePresence},
		{yaml: "boundary_approval: presence-strict\n", want: presence.ModeStrict},
		{yaml: "boundary_approval: touchid\n", err: "boundary_approval must be"},
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("%q: %v", tc.yaml, err)
			}
			continue
		}
		if err != nil || cfg.ApprovalMode() != tc.want {
			t.Fatalf("%q: %q %v", tc.yaml, cfg.ApprovalMode(), err)
		}
	}
}
