package tempfile

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestCreateSweepsOnlyStaleLeftoversOfItsOwnPattern(t *testing.T) {
	old := time.Now().Add(-2 * StaleAfter)
	fresh := time.Now().Add(-StaleAfter / 4)
	type file struct {
		name  string
		mtime time.Time
	}
	tests := []struct {
		name    string
		pattern string
		files   []file
		kept    []string
	}{
		{
			name:    "trailing star",
			pattern: "claude-limits.json.*",
			files: []file{
				{"claude-limits.json.123456", old},
				{"claude-limits.json.42", old},
				{"claude-limits.json.789", fresh},
				{"claude-limits.json", old},
				{"claude-limits.json.bak", old},
				{"claude-limits.json.12a", old},
				{"claude-limits.json.", old},
				{"other.json.123", old},
			},
			kept: []string{
				"claude-limits.json",
				"claude-limits.json.",
				"claude-limits.json.12a",
				"claude-limits.json.789",
				"claude-limits.json.bak",
				"other.json.123",
			},
		},
		{
			name:    "star before a suffix",
			pattern: ".config-*.yaml",
			files: []file{
				{".config-99.yaml", old},
				{".config-99.yaml.bak", old},
				{".config-.yaml", old},
				{".config-17.yaml", fresh},
				{"config.yaml", old},
			},
			kept: []string{".config-.yaml", ".config-17.yaml", ".config-99.yaml.bak", "config.yaml"},
		},
		{
			name:    "no star appends the random part",
			pattern: "snap.json.",
			files: []file{
				{"snap.json.5", old},
				{"snap.json", old},
			},
			kept: []string{"snap.json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				p := filepath.Join(dir, f.name)
				if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(p, f.mtime, f.mtime); err != nil {
					t.Fatal(err)
				}
			}
			tmp, err := Create(dir, tt.pattern)
			if err != nil {
				t.Fatal(err)
			}
			created := filepath.Base(tmp.Name())
			if err := tmp.Close(); err != nil {
				t.Fatal(err)
			}
			got := names(t, dir)
			want := append(append([]string{}, tt.kept...), created)
			sort.Strings(want)
			if !equal(got, want) {
				t.Fatalf("left %v, want %v", got, want)
			}
		})
	}
}

func TestCreateLeavesDirectoriesAlone(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "x.json.1")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}
	tmp, err := Create(dir, "x.json.*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmp.Close()
	if _, err := os.Stat(sub); err != nil {
		t.Fatalf("directory removed: %v", err)
	}
}

func TestCreateInMissingDirectoryFails(t *testing.T) {
	if _, err := Create(filepath.Join(t.TempDir(), "absent"), "x.*"); err == nil {
		t.Fatal("Create in a missing directory succeeded")
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
