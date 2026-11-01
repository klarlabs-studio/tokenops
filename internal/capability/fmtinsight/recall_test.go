package fmtinsight

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecall(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.fail.txt", "--- FAIL: TestLogin (0.00s)\n    login_test.go:9: got 1\nFAIL\nFAIL\texample.com/app\t0.1s\n")
	write("go.fail.json", `{"evidence":[{"kind":"failing_test","text":"TestLogin"},{"kind":"error_location","text":"login_test.go:9"}]}`)

	tests := []struct {
		name       string
		dir        string
		wantCorpus string
		minCases   int
	}{
		{"bundled corpus", "", "bundled", 50},
		{"a directory of labeled outputs", dir, dir, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := Recall(tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Corpus != tc.wantCorpus || rep.Invariant == "" || rep.Formatters == 0 {
				t.Errorf("report header = %+v", rep)
			}
			if len(rep.Levels) != 3 || rep.Levels[0].Level != "conservative" || rep.Levels[2].Level != "aggressive" {
				t.Fatalf("levels = %+v", rep.Levels)
			}
			if rep.Levels[0].Overall.Cases < tc.minCases {
				t.Errorf("cases = %d, want at least %d", rep.Levels[0].Overall.Cases, tc.minCases)
			}
			if rep.CriticalMisses == nil || len(rep.CriticalMisses) != 0 {
				t.Errorf("critical misses = %#v, want an empty list", rep.CriticalMisses)
			}
		})
	}
	if _, err := Recall(filepath.Join(dir, "missing")); err == nil {
		t.Error("a directory with no labeled outputs should be an error")
	}
}
