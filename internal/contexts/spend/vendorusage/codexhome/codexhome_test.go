package codexhome

import (
	"path/filepath"
	"testing"
)

func TestDirHonoursCodexHome(t *testing.T) {
	want := filepath.Join(t.TempDir(), "elsewhere")
	t.Setenv("CODEX_HOME", want)
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Dir() = %q, want CODEX_HOME %q", got, want)
	}
}

// Codex treats an empty CODEX_HOME as unset (codex-rs/utils/home-dir).
func TestDirTreatsEmptyCodexHomeAsUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".codex"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDirDefaultsToDotCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".codex"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestInUsesCodexHomeThenTheGivenHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	if got, want := In("/h"), filepath.Join("/h", ".codex"); got != want {
		t.Errorf("In without CODEX_HOME = %q, want %q", got, want)
	}
	t.Setenv("CODEX_HOME", "/elsewhere")
	if got := In("/h"); got != "/elsewhere" {
		t.Errorf("In with CODEX_HOME = %q, want /elsewhere", got)
	}
}

func TestOwnsRecognisesTranscriptsUnderCodexHome(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CODEX_HOME", dir)
	if !Owns(filepath.Join(dir, "sessions", "rollout-a.jsonl")) {
		t.Error("a transcript under CODEX_HOME is not Codex's")
	}
	if Owns(filepath.Join(t.TempDir(), "projects", "s.jsonl")) {
		t.Error("a Claude Code transcript is taken for Codex's")
	}
	if !Owns("/home/u/.codex/sessions/rollout-a.jsonl") {
		t.Error("a transcript under a .codex directory is not Codex's")
	}
}
