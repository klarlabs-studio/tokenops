package sourceprobe

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// isolateHome points every default root at an empty temp home, so a
// probe left on its default never reads the operator's real clients.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	return home
}

func touch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func cfgWith(codex, claude, opencode bool, root string) config.Config {
	var c config.Config
	c.VendorUsage.CodexJSONL.Enabled = codex
	c.VendorUsage.CodexJSONL.Root = root
	c.VendorUsage.ClaudeCodeJSONL.Enabled = claude
	c.VendorUsage.ClaudeCodeJSONL.Root = root
	c.VendorUsage.OpenCode.Enabled = opencode
	c.VendorUsage.OpenCode.Root = root
	return c
}

// Only enabled local readers get a probe; remote pollers have no local
// origin and are never probed.
func TestAllProbesOnlyEnabledLocalReaders(t *testing.T) {
	isolateHome(t)
	tests := []struct {
		name                    string
		codex, claude, opencode bool
		want                    []string
	}{
		{"nothing enabled", false, false, false, nil},
		{"codex only", true, false, false, []string{"codex-jsonl"}},
		{"claude only", false, true, false, []string{"claude-code-jsonl"}},
		{"opencode only", false, false, true, []string{"opencode"}},
		{"all three", true, true, true, []string{"claude-code-jsonl", "codex-jsonl", "opencode"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := All(cfgWith(tt.codex, tt.claude, tt.opencode, ""))
			keys := make([]string, 0, len(got))
			for k, p := range got {
				if p == nil {
					t.Errorf("probe %q is nil", k)
				}
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) != len(tt.want) {
				t.Fatalf("probes = %v, want %v", keys, tt.want)
			}
			for i := range keys {
				if keys[i] != tt.want[i] {
					t.Errorf("probes = %v, want %v", keys, tt.want)
				}
			}
		})
	}
}

// A configured root is read as given.
func TestAllProbesReadConfiguredRoots(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	at := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	touch(t, filepath.Join(root, "p", "s.jsonl"), at)
	probes := All(cfgWith(true, true, false, root))
	for _, tag := range []string{"codex-jsonl", "claude-code-jsonl"} {
		got, ok := probes[tag]()
		if !ok || !got.Equal(at) {
			t.Errorf("%s probe = %v, %v; want %v, true", tag, got, ok, at)
		}
	}

	db := filepath.Join(t.TempDir(), "opencode.db")
	newOpencodeDB(t, db, `CREATE TABLE message (id TEXT, time_created INTEGER)`, at.UnixMilli())
	var oc config.Config
	oc.VendorUsage.OpenCode.Enabled = true
	oc.VendorUsage.OpenCode.Root = db
	if got, ok := All(oc)["opencode"](); !ok || !got.Equal(at) {
		t.Errorf("opencode probe = %v, %v; want %v, true", got, ok, at)
	}
}

// An unset root resolves each client's default under HOME. With nothing
// installed, every origin is unknown rather than empty.
func TestAllProbesResolveDefaultsUnderHome(t *testing.T) {
	home := isolateHome(t)
	probes := All(cfgWith(true, true, true, ""))
	for tag, probe := range probes {
		if at, ok := probe(); ok || !at.IsZero() {
			t.Errorf("%s probe with nothing installed = %v, %v; want unknown", tag, at, ok)
		}
	}

	at := time.Date(2026, 9, 2, 9, 30, 0, 0, time.UTC)
	touch(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "02", "rollout-x.jsonl"), at)
	touch(t, filepath.Join(home, ".claude", "projects", "proj", "s.jsonl"), at.Add(time.Minute))
	ocPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(ocPath), 0o750); err != nil {
		t.Fatal(err)
	}
	newOpencodeDB(t, ocPath, `CREATE TABLE message (id TEXT, time_created INTEGER)`, at.Add(2*time.Minute).UnixMilli())

	want := map[string]time.Time{
		"codex-jsonl":       at,
		"claude-code-jsonl": at.Add(time.Minute),
		"opencode":          at.Add(2 * time.Minute),
	}
	for tag, w := range want {
		got, ok := probes[tag]()
		if !ok || !got.Equal(w) {
			t.Errorf("%s probe = %v, %v; want %v, true", tag, got, ok, w)
		}
	}
}

// No home to resolve a default under means no origin to read: unknown,
// never a crash and never a guess.
func TestAllProbesWithoutHomeAreUnknown(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("home resolution differs here")
	}
	isolateHome(t)
	t.Setenv("HOME", "")
	for tag, probe := range All(cfgWith(true, true, true, "")) {
		if at, ok := probe(); ok || !at.IsZero() {
			t.Errorf("%s probe without HOME = %v, %v; want unknown", tag, at, ok)
		}
	}
}

func TestNewestJSONLAt(t *testing.T) {
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

	t.Run("empty root is unknown", func(t *testing.T) {
		if at, ok := newestJSONLAt(""); ok || !at.IsZero() {
			t.Errorf("= %v, %v", at, ok)
		}
	})
	t.Run("missing root is unknown", func(t *testing.T) {
		if at, ok := newestJSONLAt(filepath.Join(t.TempDir(), "absent")); ok || !at.IsZero() {
			t.Errorf("= %v, %v", at, ok)
		}
	})
	t.Run("root that is a file is unknown", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.jsonl")
		touch(t, f, base)
		if at, ok := newestJSONLAt(f); ok || !at.IsZero() {
			t.Errorf("= %v, %v", at, ok)
		}
	})
	t.Run("existing root without transcripts is a real empty", func(t *testing.T) {
		root := t.TempDir()
		touch(t, filepath.Join(root, "notes.txt"), base)
		if at, ok := newestJSONLAt(root); !ok || !at.IsZero() {
			t.Errorf("= %v, %v; want zero, true", at, ok)
		}
	})
	t.Run("newest transcript anywhere under root wins", func(t *testing.T) {
		root := t.TempDir()
		touch(t, filepath.Join(root, "a", "old.jsonl"), base)
		touch(t, filepath.Join(root, "b", "c", "d", "new.jsonl"), base.Add(2*time.Hour))
		touch(t, filepath.Join(root, "mid.jsonl"), base.Add(time.Hour))
		touch(t, filepath.Join(root, "newer-but-not-a-transcript.json"), base.Add(5*time.Hour))
		if err := os.MkdirAll(filepath.Join(root, "dir.jsonl"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(root, "dir.jsonl"), base.Add(9*time.Hour), base.Add(9*time.Hour)); err != nil {
			t.Fatal(err)
		}
		at, ok := newestJSONLAt(root)
		if !ok || !at.Equal(base.Add(2*time.Hour)) || at.Location() != time.UTC {
			t.Errorf("= %v, %v; want %v in UTC", at, ok, base.Add(2*time.Hour))
		}
	})
	t.Run("unreadable subtree is skipped, not fatal", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("permission bits are not enforced here")
		}
		root := t.TempDir()
		touch(t, filepath.Join(root, "ok", "s.jsonl"), base)
		locked := filepath.Join(root, "locked")
		touch(t, filepath.Join(locked, "hidden.jsonl"), base.Add(time.Hour))
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })
		at, ok := newestJSONLAt(root)
		if !ok || !at.Equal(base) {
			t.Errorf("= %v, %v; want %v, true", at, ok, base)
		}
	})
}

func newOpencodeDB(t *testing.T, path, schema string, times ...int64) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	for i, ms := range times {
		if _, err := db.Exec(`INSERT INTO message (id, time_created) VALUES (?, ?)`, i, ms); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewestOpenCodeMessage(t *testing.T) {
	at := time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC)
	t.Run("empty path is unknown", func(t *testing.T) {
		if got, ok := newestOpenCodeMessage(""); ok || !got.IsZero() {
			t.Errorf("= %v, %v", got, ok)
		}
	})
	t.Run("missing store is unknown", func(t *testing.T) {
		if got, ok := newestOpenCodeMessage(filepath.Join(t.TempDir(), "absent.db")); ok || !got.IsZero() {
			t.Errorf("= %v, %v", got, ok)
		}
	})
	t.Run("newest message wins", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "opencode.db")
		newOpencodeDB(t, db, `CREATE TABLE message (id TEXT, time_created INTEGER)`,
			at.Add(-time.Hour).UnixMilli(), at.UnixMilli(), at.Add(-2*time.Hour).UnixMilli())
		if got, ok := newestOpenCodeMessage(db); !ok || !got.Equal(at) {
			t.Errorf("= %v, %v; want %v, true", got, ok, at)
		}
	})
	t.Run("readable store with no messages is a real empty", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "opencode.db")
		newOpencodeDB(t, db, `CREATE TABLE message (id TEXT, time_created INTEGER)`)
		if got, ok := newestOpenCodeMessage(db); !ok || !got.IsZero() {
			t.Errorf("= %v, %v; want zero, true", got, ok)
		}
	})
}
