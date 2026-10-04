package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An upgrade's refresh never installs an app nobody asked for, and a
// binary without the app beside it says where the app comes from.
func TestInstallMenubarRefreshAndMissing(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "Applications", menubarApp)
	src := filepath.Join(dir, "shipped", menubarApp)
	if err := os.MkdirAll(filepath.Join(src, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := installMenubar(&out, src, dst, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("refresh installed an app that was never installed")
	}
	err := installMenubar(&out, "", dst, false)
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Errorf("missing app: %v", err)
	}
}
