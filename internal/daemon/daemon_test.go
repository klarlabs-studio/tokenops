package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCertDirPreservesExplicit(t *testing.T) {
	got, err := resolveCertDir("/custom/certs")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/certs" {
		t.Errorf("resolveCertDir = %q, want /custom/certs", got)
	}
}

func TestResolveCertDirFallsBackToDefault(t *testing.T) {
	got, err := resolveCertDir("")
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join(".tokenops", "certs")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("resolveCertDir('') = %q, want suffix %q", got, wantSuffix)
	}
}

func TestResolveStoragePathPreservesExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom", "events.db")
	got, err := resolveStoragePath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Errorf("resolveStoragePath = %q, want %q", got, path)
	}
	// Verify the parent directory was created.
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		t.Error("parent directory was not created")
	}
}

func TestResolveStoragePathFallsBackToDefault(t *testing.T) {
	got, err := resolveStoragePath("")
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join(".tokenops", "events.db")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("resolveStoragePath('') = %q, want suffix %q", got, wantSuffix)
	}
}

func TestResolveStoragePathCreatesParentDir(t *testing.T) {
	tmp := t.TempDir()
	got, err := resolveStoragePath(filepath.Join(tmp, "sub", "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(tmp, "sub", "events.db") {
		t.Errorf("resolveStoragePath = %q", got)
	}
}
