package sourceprobe

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The probes find a tool by its app bundle, its directory under the home
// or XDG roots, or its CLI on PATH; nothing outside the temp machine is
// looked at, and nothing found is a file's contents.
func TestInstalledTools(t *testing.T) {
	home := isolateHome(t)
	apps := t.TempDir()
	xdgData := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdgData)
	mkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	mkdir(filepath.Join(apps, "Cursor.app", "Contents"))
	mkdir(filepath.Join(home, ".cursor"))
	mkdir(filepath.Join(home, ".config", "github-copilot"))
	mkdir(filepath.Join(xdgData, "kilo"))
	// A file where a directory is looked for is not the tool.
	if err := os.WriteFile(filepath.Join(home, ".factory"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	onPath := map[string]bool{"cursor-agent": true, "amp": true}
	m := Machine{Home: home, Getenv: os.Getenv, AppDirs: []string{apps},
		LookPath: func(name string) (string, error) {
			if onPath[name] {
				return "/opt/bin/" + name, nil
			}
			return "", errors.New("not found")
		}}
	got := InstalledTools(m)
	want := map[string][]string{
		"cursor": {filepath.Join(apps, "Cursor.app"), "~/.cursor", "cursor-agent on PATH"},
		"github": {"~/.config/github-copilot"},
		"amp":    {"amp on PATH"},
		"kilo":   {filepath.Join(xdgData, "kilo")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InstalledTools = %v\nwant %v", got, want)
	}
}

// With no home known, only binaries are looked for, and nothing is
// resolved against the process's working directory.
func TestInstalledToolsWithoutHome(t *testing.T) {
	isolateHome(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	got := InstalledTools(Machine{LookPath: func(string) (string, error) { return "", errors.New("no") }})
	if len(got) != 0 {
		t.Errorf("InstalledTools = %v, want nothing", got)
	}
}
