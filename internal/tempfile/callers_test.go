package tempfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every atomic writer creates its temp file through Create, so a writer
// killed before its rename has its leftover cleared by the next write. A
// direct os.CreateTemp in the module's code would leak again.
func TestNoWriterBypassesCreate(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	for _, top := range []string{"cmd", "internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if rel == filepath.Join("internal", "tempfile", "tempfile.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "os.CreateTemp(") {
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these create temp files without tempfile.Create, so a killed writer's leftover is never cleared: %v", offenders)
	}
}
