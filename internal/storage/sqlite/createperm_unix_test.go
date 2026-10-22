//go:build unix

package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A new store must never exist with the umask default, not even during
// Open: the database, its WAL and its shared-memory file hold every
// session's usage history. restrictToOwner used to narrow them only
// after the schema had been written, leaving a window in which other
// users could open them.
func TestOpenCreatesStoreOwnerOnlyFromTheStart(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	path := filepath.Join(t.TempDir(), "events.db")
	var observed bool
	testHookBeforeRestrict = func(p string) {
		observed = true
		for _, f := range []string{p, p + "-wal", p + "-shm"} {
			info, err := os.Stat(f)
			if err != nil {
				if f == p {
					t.Errorf("store file missing during open: %v", err)
				}
				continue
			}
			if mode := info.Mode().Perm(); mode != ownerOnly {
				t.Errorf("%s mode during open = %o, want %o", filepath.Base(f), mode, ownerOnly)
			}
		}
	}
	t.Cleanup(func() { testHookBeforeRestrict = nil })

	s, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = s.Close()
	if !observed {
		t.Fatal("hook not called")
	}
}
