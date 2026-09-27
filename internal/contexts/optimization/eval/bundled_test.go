package eval

import (
	"context"
	"os"
	"testing"
)

// Release binaries are built with -trimpath, so a suite path derived from
// the source file's location names a Go module path, not a directory, and
// `tokenops eval` / tokenops_eval failed on every installed release. The
// bundled suites travel inside the binary instead.
func TestBundledSuitesTravelWithTheBinary(t *testing.T) {
	suites, err := BundledSuites()
	if err != nil {
		t.Fatalf("BundledSuites: %v", err)
	}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".json" {
			want++
		}
	}
	if len(suites) != want || want == 0 {
		t.Fatalf("bundled %d suites, testdata has %d", len(suites), want)
	}
	for _, s := range suites {
		if s.Name == "" || len(s.Cases) == 0 {
			t.Errorf("bundled suite %+v has no name or cases", s.Name)
		}
	}
}

func TestRunDefaultsToBundledSuites(t *testing.T) {
	t.Chdir(t.TempDir())
	res, err := Run(context.Background(), RunParams{})
	if err != nil {
		t.Fatalf("Run with no suite path: %v", err)
	}
	if res.Report == nil || res.Report.TotalCases == 0 {
		t.Fatalf("default run executed no cases: %+v", res.Report)
	}
}
