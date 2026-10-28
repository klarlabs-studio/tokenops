package claudehome

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// isolate points HOME, the managed-settings directory and the config-dir
// variable at fixtures, so nothing on the machine leaks into a test.
func isolate(t *testing.T) (home, managed string) {
	t.Helper()
	home = t.TempDir()
	managed = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvVar, "")
	prev := ManagedDir
	ManagedDir = managed
	t.Cleanup(func() { ManagedDir = prev })
	return home, managed
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDirDefaultsToHomeClaude(t *testing.T) {
	home, _ := isolate(t)
	if got, want := Dir(), filepath.Join(home, ".claude"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDirHonorsConfigDirVariable(t *testing.T) {
	isolate(t)
	t.Setenv(EnvVar, "/srv/tenant-a")
	if got := Dir(); got != "/srv/tenant-a" {
		t.Errorf("Dir() = %q, want the CLAUDE_CONFIG_DIR value", got)
	}
	if got := Join("projects"); got != filepath.Join("/srv/tenant-a", "projects") {
		t.Errorf("Join(projects) = %q", got)
	}
}

func TestDirReadsConfigDirFromUserSettingsEnv(t *testing.T) {
	home, _ := isolate(t)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env":{"CLAUDE_CONFIG_DIR":"/u/claude-work"}}`)
	if got := Dir(); got != "/u/claude-work" {
		t.Errorf("Dir() = %q, want the user settings env value", got)
	}
}

// Managed settings rank above the user's file, so their env value wins
// over the user's when both set it.
func TestDirManagedSettingsEnvBeatsUserSettingsEnv(t *testing.T) {
	home, managed := isolate(t)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env":{"CLAUDE_CONFIG_DIR":"/u/mine"}}`)
	writeFile(t, filepath.Join(managed, "managed-settings.json"), `{"env":{"CLAUDE_CONFIG_DIR":"/corp/claude"}}`)
	if got := Dir(); got != "/corp/claude" {
		t.Errorf("Dir() = %q, want the managed value", got)
	}
}

// The process environment is read before any settings file, as Claude
// Code itself does.
func TestDirProcessEnvBeatsSettingsEnv(t *testing.T) {
	home, managed := isolate(t)
	writeFile(t, filepath.Join(managed, "managed-settings.json"), `{"env":{"CLAUDE_CONFIG_DIR":"/corp/claude"}}`)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env":{"CLAUDE_CONFIG_DIR":"/u/mine"}}`)
	t.Setenv(EnvVar, "/shell/claude")
	if got := Dir(); got != "/shell/claude" {
		t.Errorf("Dir() = %q, want the shell value", got)
	}
}

// Drop-ins merge after managed-settings.json in alphabetical order, so the
// last one wins. Hidden and non-JSON files are not read.
func TestSettingsFilesHighestPrecedenceFirst(t *testing.T) {
	home, managed := isolate(t)
	writeFile(t, filepath.Join(managed, "managed-settings.json"), `{}`)
	writeFile(t, filepath.Join(managed, "managed-settings.d", "10-telemetry.json"), `{}`)
	writeFile(t, filepath.Join(managed, "managed-settings.d", "20-security.json"), `{}`)
	writeFile(t, filepath.Join(managed, "managed-settings.d", ".hidden.json"), `{}`)
	writeFile(t, filepath.Join(managed, "managed-settings.d", "notes.txt"), `{}`)
	got := SettingsFiles()
	want := []string{
		filepath.Join(managed, "managed-settings.d", "20-security.json"),
		filepath.Join(managed, "managed-settings.d", "10-telemetry.json"),
		filepath.Join(managed, "managed-settings.json"),
		filepath.Join(home, ".claude", "settings.json"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SettingsFiles() =\n %v\nwant\n %v", got, want)
	}
}
