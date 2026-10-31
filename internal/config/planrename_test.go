package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A config written before Anthropic renamed Max 20x to Ultra and Max 5x to
// Max is rewritten in place: the plan values change, and every other byte
// of the file, comments included, stays as the operator left it.
func TestRenameRetiredPlansKeepsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := "# my tokenops config\n" +
		"plans:\n" +
		"  anthropic: claude-max-20x   # the $200 one\n" +
		"  openai: gpt-pro-5x\n" +
		"preferred_models:\n" +
		"  anthropic: claude-max-20x\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	renamed, err := RenameRetiredPlans(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(renamed) != 1 || renamed[0] != (PlanRename{Provider: "anthropic", From: "claude-max-20x", To: "claude-ultra"}) {
		t.Errorf("renamed = %+v", renamed)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Only the binding under plans: changes; the same text elsewhere is not
	// a plan binding.
	want := "# my tokenops config\n" +
		"plans:\n" +
		"  anthropic: claude-ultra   # the $200 one\n" +
		"  openai: gpt-pro-5x\n" +
		"preferred_models:\n" +
		"  anthropic: claude-max-20x\n"
	if string(got) != want {
		t.Errorf("config =\n%s\nwant\n%s", got, want)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != ConfigFilePerm {
		t.Errorf("mode = %v, %v; want %v", st.Mode().Perm(), err, ConfigFilePerm)
	}

	// Run again, nothing is left to rename and the file is not written.
	renamed, err = RenameRetiredPlans(path)
	if err != nil || len(renamed) != 0 {
		t.Errorf("second run = %+v, %v", renamed, err)
	}
	if renamed, err := RenameRetiredPlans(filepath.Join(t.TempDir(), "absent.yaml")); err != nil || renamed != nil {
		t.Errorf("missing file = %+v, %v", renamed, err)
	}
}

// Every read of the config means the plan the old name became, before any
// file is rewritten.
func TestLoadReadsRetiredPlansByTheirCurrentName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("plans:\n  anthropic: claude-max-5x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plans["anthropic"] != "claude-max" {
		t.Errorf("Load: plan = %q, want claude-max", cfg.Plans["anthropic"])
	}
	mut, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if mut.Plans["anthropic"] != "claude-max" {
		t.Errorf("ReadMutable: plan = %q, want claude-max", mut.Plans["anthropic"])
	}
	if b, _ := os.ReadFile(path); string(b) != "plans:\n  anthropic: claude-max-5x\n" {
		t.Errorf("reading wrote the file: %q", b)
	}
}
