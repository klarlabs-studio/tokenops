package sqliteuri

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every SQLite file is opened through File, so a path holding "%", "?" or
// "#" opens the file it names. A file: URI built by hand opened another
// file once: a test store at "x#1.db" shared one with "x".
func TestNoCallerBuildsAFileURI(t *testing.T) {
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
			if rel == filepath.Join("internal", "sqliteuri", "sqliteuri.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "\"file:\"") {
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these build a file: URI by hand instead of with sqliteuri.File, so a path with %%, ? or # opens another file: %v", offenders)
	}
}
