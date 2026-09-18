package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// An operator who runs `tokenops init` from their home directory — the most
// likely place a first-time user runs it — silently bound the rule scanner to
// their entire home directory, and repo_id to their username. The scanner
// walks that root recursively.
func TestRulesRootRefusesTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	res := resolveRulesRoot(rulesRootInput{Cwd: home, Home: home})
	if res.Enabled {
		t.Fatalf("rules must not be enabled against the home directory: %+v", res)
	}
	if res.Reason == "" {
		t.Error("want a stated reason, not a silent disable")
	}
}

// A project is what the operator means by "here", and the repo root is where
// it starts — not whichever subdirectory the shell happened to be in.
func TestRulesRootFindsTheRepoRootFromASubdirectory(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "work", "myproject")
	deep := filepath.Join(repo, "internal", "cli")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	res := resolveRulesRoot(rulesRootInput{Cwd: deep, Home: home})
	if !res.Enabled {
		t.Fatalf("a repo is exactly what rules should scan: %+v", res)
	}
	if res.Root != repo {
		t.Errorf("Root = %q, want the repo root %q", res.Root, repo)
	}
	if res.RepoID != "myproject" {
		t.Errorf("RepoID = %q, want myproject", res.RepoID)
	}
}

// Not every project is a git repo. A plain directory that is not home is
// still a reasonable scan root, which is the behaviour that already worked.
func TestRulesRootAcceptsAPlainDirectory(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	res := resolveRulesRoot(rulesRootInput{Cwd: dir, Home: home})
	if !res.Enabled || res.Root != dir {
		t.Fatalf("want the directory itself: %+v", res)
	}
	if res.RepoID != "notes" {
		t.Errorf("RepoID = %q, want notes", res.RepoID)
	}
}

// A dotfiles repo at $HOME must not promote a subdirectory's scan up to the
// whole home directory. Scanning the bounded subdirectory is fine — walking
// home is the hazard, and a .git there does not make it smaller.
func TestRulesRootDoesNotPromoteToAHomeLevelRepo(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res := resolveRulesRoot(rulesRootInput{Cwd: sub, Home: home})
	if res.Root == home {
		t.Fatalf("scan root was promoted to the home directory: %+v", res)
	}
	if res.Enabled && res.Root != sub {
		t.Fatalf("want the bounded subdirectory, got %+v", res)
	}
}

// The filesystem root is the same hazard, one size up.
func TestRulesRootRefusesTheFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	if res := resolveRulesRoot(rulesRootInput{Cwd: root, Home: "/home/someone"}); res.Enabled {
		t.Fatalf("must not scan the filesystem root: %+v", res)
	}
}

// An explicit --rules-root is the operator's own call and is honoured, even
// when it is somewhere the default would have refused.
func TestRulesRootHonoursAnExplicitOverride(t *testing.T) {
	home := t.TempDir()
	res := resolveRulesRoot(rulesRootInput{Cwd: home, Home: home, RootOverride: home})
	if !res.Enabled {
		t.Fatalf("an explicit override is a decision, not an accident: %+v", res)
	}
	if res.Root != home {
		t.Errorf("Root = %q, want %q", res.Root, home)
	}
}

func TestRulesRootHonoursAnExplicitRepoID(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	res := resolveRulesRoot(rulesRootInput{Cwd: dir, Home: home, RepoOverride: "custom"})
	if res.RepoID != "custom" {
		t.Errorf("RepoID = %q, want custom", res.RepoID)
	}
}
