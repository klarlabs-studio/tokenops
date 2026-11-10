package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// seedConfig writes body as the config in a new directory.
func seedConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A change writes what it changed. A key this binary does not know (a
// newer TokenOps wrote it, and an older process is still running), the
// operator's comments and the settings left at their defaults are not the
// change's to rewrite.
func TestWriteMutableKeepsWhatTheChangeDoesNotTouch(t *testing.T) {
	path := seedConfig(t, "# my tokenops config\n"+
		"listen: 127.0.0.1:7878   # local only\n"+
		"preferred_models:\n"+
		"    anthropic: claude-opus-5   # the one I like\n"+
		"a_setting_from_a_newer_version:\n"+
		"    keep: me\n")

	cfg, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PreferredModels["anthropic"] = "claude-sonnet-5"
	cfg.PreferredModels["openai"] = "gpt-5"
	if err := WriteMutable(path, cfg); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}

	want := "# my tokenops config\n" +
		"listen: 127.0.0.1:7878 # local only\n" +
		"preferred_models:\n" +
		"    anthropic: claude-sonnet-5 # the one I like\n" +
		"    openai: gpt-5\n" +
		"a_setting_from_a_newer_version:\n" +
		"    keep: me\n"
	if got := readFileString(t, path); got != want {
		t.Errorf("config after the change:\n%s\nwant:\n%s", got, want)
	}
}

// A setting taken away is taken out of the file, and a section the file
// did not have gets the key that changed, not every default beside it.
func TestWriteMutableRemovesAndAddsOnlyWhatChanged(t *testing.T) {
	path := seedConfig(t, "preferred_models:\n"+
		"    anthropic: claude-opus-5\n"+
		"    openai: gpt-5\n")

	cfg, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	delete(cfg.PreferredModels, "openai")
	cfg.Log.Level = "debug"
	if err := WriteMutable(path, cfg); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}

	want := "preferred_models:\n" +
		"    anthropic: claude-opus-5\n" +
		"log:\n" +
		"    level: debug\n"
	if got := readFileString(t, path); got != want {
		t.Errorf("config after the change:\n%s\nwant:\n%s", got, want)
	}
	again, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Log.Level != "debug" || again.Log.Format != Default().Log.Format {
		t.Errorf("log = %+v, want level debug and the default format", again.Log)
	}
}

// Two processes read the config, each changes something else, and both
// write. The second write used to put back everything it had read, and
// with it undo the first.
func TestWriteMutableKeepsAChangeMadeSinceTheRead(t *testing.T) {
	path := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")

	first, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	first.PreferredModels["openai"] = "gpt-5"
	first.AllowedHosts = []string{"tokenops.internal"}
	second.PreferredModels["gemini"] = "gemini-3-pro"
	second.Log.Level = "debug"
	for _, cfg := range []Config{first, second} {
		if err := WriteMutable(path, cfg); err != nil {
			t.Fatalf("WriteMutable: %v", err)
		}
	}

	got, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	wantModels := map[string]string{"anthropic": "claude-opus-5", "openai": "gpt-5", "gemini": "gemini-3-pro"}
	if fmt.Sprint(got.PreferredModels) != fmt.Sprint(wantModels) {
		t.Errorf("preferred_models = %v, want %v", got.PreferredModels, wantModels)
	}
	if !slices.Equal(got.AllowedHosts, []string{"tokenops.internal"}) || got.Log.Level != "debug" {
		t.Errorf("allowed_hosts = %v, log level = %q; want both writers' changes", got.AllowedHosts, got.Log.Level)
	}
}

// Where both changed the same thing, neither is dropped without a word:
// the second write is refused, names the key, and leaves the file as the
// first wrote it. A list is one thing; the keys of a mapping are not.
func TestWriteMutableRefusesAChangeTheFileAlreadyChanged(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second func(*Config)
		key           string
	}{
		{
			name:   "a value",
			first:  func(c *Config) { c.PreferredModels["anthropic"] = "claude-sonnet-5" },
			second: func(c *Config) { c.PreferredModels["anthropic"] = "claude-haiku-5" },
			key:    "preferred_models.anthropic",
		},
		{
			name:   "a list",
			first:  func(c *Config) { c.AllowedHosts = append(c.AllowedHosts, "one.internal") },
			second: func(c *Config) { c.AllowedHosts = append(c.AllowedHosts, "two.internal") },
			key:    "allowed_hosts",
		},
		{
			name:   "a value taken away",
			first:  func(c *Config) { c.PreferredModels["anthropic"] = "claude-sonnet-5" },
			second: func(c *Config) { delete(c.PreferredModels, "anthropic") },
			key:    "preferred_models.anthropic",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")
			first, err := ReadMutable(path)
			if err != nil {
				t.Fatal(err)
			}
			second, err := ReadMutable(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.first(&first)
			tc.second(&second)
			if err := WriteMutable(path, first); err != nil {
				t.Fatalf("first write: %v", err)
			}
			afterFirst := readFileString(t, path)

			err = WriteMutable(path, second)
			var changed *ChangedError
			if !errors.As(err, &changed) || !slices.Equal(changed.Keys, []string{tc.key}) {
				t.Fatalf("second write: err = %v, want a ChangedError for %s", err, tc.key)
			}
			if got := readFileString(t, path); got != afterFirst {
				t.Errorf("a refused write changed the file:\n%s\nwant:\n%s", got, afterFirst)
			}
		})
	}
}

// Taking the last setting out of a section empties it for the writer, who
// then leaves the section out. A setting another process put in the
// section meanwhile is not the writer's to take out with it.
func TestWriteMutableEmptyingASectionKeepsWhatWasAddedToIt(t *testing.T) {
	path := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")
	first, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	first.PreferredModels["openai"] = "gpt-5"
	delete(second.PreferredModels, "anthropic")
	for _, cfg := range []Config{first, second} {
		if err := WriteMutable(path, cfg); err != nil {
			t.Fatalf("WriteMutable: %v", err)
		}
	}
	if got, want := readFileString(t, path), "preferred_models:\n    openai: gpt-5\n"; got != want {
		t.Errorf("config = %q, want %q", got, want)
	}
}

// Both wanting the same value is not a disagreement.
func TestWriteMutableAcceptsTheSameChangeTwice(t *testing.T) {
	path := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")
	first, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []Config{first, second} {
		cfg.PreferredModels["anthropic"] = "claude-sonnet-5"
		if err := WriteMutable(path, cfg); err != nil {
			t.Fatalf("WriteMutable: %v", err)
		}
	}
}

// A hand edit that no longer parses is the operator's to mend. Writing
// over it would discard it.
func TestWriteMutableLeavesAFileThatNoLongerParses(t *testing.T) {
	path := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")
	cfg, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Log.Level = "debug"
	broken := "preferred_models:\n  anthropic: [half an edit\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteMutable(path, cfg); err == nil {
		t.Fatal("WriteMutable wrote over a file that no longer parses")
	}
	if got := readFileString(t, path); got != broken {
		t.Errorf("file = %q, want it left as %q", got, broken)
	}
}

// The config is replaced in one step, at its permissions, and nothing but
// the lock is left beside it.
func TestWriteMutableReplacesTheFileWhole(t *testing.T) {
	path := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // the mode the write has to tighten
		t.Fatal(err)
	}
	cfg, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Log.Level = "debug"
	if err := WriteMutable(path, cfg); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != ConfigFilePerm {
		t.Errorf("config mode = %#o, want %#o", fi.Mode().Perm(), ConfigFilePerm)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{".config.yaml.lock", "config.yaml"}; !slices.Equal(names, want) {
		t.Errorf("files beside the config = %v, want %v", names, want)
	}
}

// A config kept as a symlink into a dotfiles repository stays a symlink:
// the file it points at is the one rewritten.
func TestWriteMutableWritesThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	target := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")
	link := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadMutable(link)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Log.Level = "debug"
	if err := WriteMutable(link, cfg); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	if got := readFileString(t, target); !strings.Contains(got, "level: debug") {
		t.Errorf("the file the symlink points at was not written:\n%s", got)
	}
}

// Many writers at once, each reading and then changing a key of its own:
// every change is in the file at the end, and no reader ever saw half a
// file.
func TestWriteMutableConcurrentWritersAllLand(t *testing.T) {
	path := seedConfig(t, "preferred_models:\n    anthropic: claude-opus-5\n")
	const writers = 24

	// A reader beside them, as the daemon and the status line are.
	stop := make(chan struct{})
	torn := make(chan error, 1)
	go func() {
		defer close(torn)
		for {
			select {
			case <-stop:
				return
			default:
			}
			cfg, err := ReadMutable(path)
			if err == nil && cfg.PreferredModels["anthropic"] == "" {
				err = errors.New("the config was read without what it has held throughout")
			}
			if err != nil {
				torn <- err
				return
			}
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			cfg, err := ReadMutable(path)
			if err != nil {
				errs <- fmt.Errorf("writer %d read: %w", i, err)
				return
			}
			cfg.PreferredModels[fmt.Sprintf("provider-%02d", i)] = "a-model"
			if err := WriteMutable(path, cfg); err != nil {
				errs <- fmt.Errorf("writer %d write: %w", i, err)
			}
		})
	}
	wg.Wait()
	close(stop)
	if err := <-torn; err != nil {
		t.Errorf("a reader during the writes: %v", err)
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	got, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PreferredModels) != writers+1 {
		t.Errorf("%d preferred models in the file, want %d: a writer's change was lost", len(got.PreferredModels), writers+1)
	}
}

// A config that was not read from the file replaces it: that is how a new
// one is written.
func TestWriteMutableWritesAConfigFromElsewhereWhole(t *testing.T) {
	path := seedConfig(t, "a_setting_from_a_newer_version: true\n")
	cfg := Default()
	cfg.PreferredModels = map[string]string{"anthropic": "claude-opus-5"}
	if err := WriteMutable(path, cfg); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}
	got := readFileString(t, path)
	if strings.Contains(got, "a_setting_from_a_newer_version") || !strings.Contains(got, "anthropic: claude-opus-5") {
		t.Errorf("config was not replaced:\n%s", got)
	}
}
