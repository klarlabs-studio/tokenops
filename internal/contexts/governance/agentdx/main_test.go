package agentdx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
)

// TestMain wires the opencode reader the composition root wires. The
// opencode fixtures are real opencode.db files in both shapes, so they
// are read through the real adapter rather than a fake of it; Cursor's
// tests install their own in-memory store per test.
func TestMain(m *testing.M) {
	UseOpencodeStore(opencodestore.Store{})
	os.Exit(m.Run())
}

// An opencode store on disk with no reader wired is reported, never read
// as an operator who did not use opencode; no store at all is not an
// error.
func TestOpencodeWithoutAReaderIsReported(t *testing.T) {
	UseOpencodeStore(nil)
	t.Cleanup(func() { UseOpencodeStore(opencodestore.Store{}) })
	dir := t.TempDir()
	if recs, err := ExtractOpencode(ExtractOptions{Root: filepath.Join(dir, "missing.db")}); err != nil || recs != nil {
		t.Fatalf("absent store: %v, %v", recs, err)
	}
	path := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractOpencode(ExtractOptions{Root: path}); !errors.Is(err, ErrNoOpencodeStore) {
		t.Fatalf("err = %v, want ErrNoOpencodeStore", err)
	}
}
