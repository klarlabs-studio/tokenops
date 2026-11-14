package config

import (
	"os"
	"path/filepath"
	"testing"
)

// storage.incremental_reads is on unless the file or the environment
// turns it off; the environment wins over the file.
func TestStorageIncrementalReads(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		env  string
		want bool
	}{
		{name: "default", want: true},
		{name: "file off", yaml: "storage:\n  incremental_reads: false\n", want: false},
		{name: "file on", yaml: "storage:\n  incremental_reads: true\n", want: true},
		{name: "env off", env: "false", want: false},
		{name: "env 0", env: "0", want: false},
		{name: "env on over file off", yaml: "storage:\n  incremental_reads: false\n", env: "on", want: true},
		{name: "env off over file on", yaml: "storage:\n  incremental_reads: true\n", env: "no", want: false},
		{name: "env not a bool leaves the file's", yaml: "storage:\n  incremental_reads: false\n", env: "maybe", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
			t.Setenv("TOKENOPS_STORAGE_INCREMENTAL_READS", tc.env)
			path := ""
			if tc.yaml != "" {
				path = filepath.Join(dir, "config.yaml")
				if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Storage.ReadsIncrementally(); got != tc.want {
				t.Fatalf("ReadsIncrementally() = %v, want %v", got, tc.want)
			}
		})
	}
}
