package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The store is opened through a file: URI, where '#' starts a fragment,
// '?' the query and '%' an escape. A path holding one of them opened
// another file: "a#1.db" opened "a", so two stores could share one file.
// Each must open exactly the file it names, read-write and read-only.
func TestOpenUsesThePathAsNamed(t *testing.T) {
	for _, name := range []string{"a#1.db", "b?mode=ro.db", "c%41.db", "d e.db"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			s, err := Open(context.Background(), path, Options{})
			if err != nil {
				t.Fatalf("open %q: %v", name, err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if n := e.Name(); n != name && n != name+"-wal" && n != name+"-shm" {
					t.Errorf("opening %q created %q", name, n)
				}
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the named file was not created: %v", err)
			}
			ro, err := OpenReadOnly(context.Background(), path)
			if err != nil {
				t.Fatalf("open read-only %q: %v", name, err)
			}
			_ = ro.Close()
		})
	}
}
