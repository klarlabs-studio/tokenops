package sourceprobe

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// An opencode that was installed and never used, or was reset, leaves an
// empty opencode.db at its default path. The probe has to read that as a
// store holding nothing, or the source is graded on how long ago its last
// event was and reported as critical on a machine that stopped using it.
func TestAnEmptyDefaultOpenCodeStoreIsAKnownEmpty(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{"zero-byte file", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := isolateHome(t)
			path := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			var cfg config.Config
			cfg.VendorUsage.OpenCode.Enabled = true

			at, known := All(cfg)["opencode"]()
			if !known || !at.IsZero() {
				t.Errorf("probe = %v, %v; want zero, true", at, known)
			}
		})
	}
}
