package cli

import (
	"os"
	"path/filepath"
)

// rulesRootInput is what deciding a scan root depends on, injected so the
// decision is testable without moving the test process around the
// filesystem.
type rulesRootInput struct {
	// Cwd is where `tokenops init` was run.
	Cwd string
	// Home is the operator's home directory.
	Home string
	// RootOverride and RepoOverride are --rules-root and --repo-id.
	RootOverride string
	RepoOverride string
}

// rulesRootResult is the decision: where to scan, what to call it, and
// whether to scan at all.
type rulesRootResult struct {
	Root    string
	RepoID  string
	Enabled bool
	// Reason explains a disabled result in the operator's terms. Empty
	// when Enabled.
	Reason string
}

// resolveRulesRoot decides where rule intelligence should scan.
//
// It used to be `os.Getwd()` with no further thought, which is fine from
// inside a project and wrong everywhere else. An operator who ran `tokenops
// init` from their home directory — the most likely place a first-time user
// runs anything — bound the scanner to their entire home directory and got
// `repo_id: <their username>`, with nothing said about it. The scanner walks
// its root recursively, so that is every project, every cache and every
// Library folder they own.
//
// So: find the project, and refuse the places that are not one.
//
//   - An explicit --rules-root is the operator's own decision and is taken as
//     given, wherever it points.
//   - Otherwise walk up from the cwd for a .git directory and use the repo
//     root, because that is what someone means by "this project" — not
//     whichever subdirectory their shell was in.
//   - A cwd that is the home directory or the filesystem root is refused, and
//     rules are left off with the reason stated. A dotfiles repo at $HOME is
//     still $HOME: a .git there does not make the walk smaller.
//   - Any other directory is taken as-is, which is the behaviour that already
//     worked.
func resolveRulesRoot(in rulesRootInput) rulesRootResult {
	repoID := func(root string) string {
		if in.RepoOverride != "" {
			return in.RepoOverride
		}
		return filepath.Base(root)
	}

	if in.RootOverride != "" {
		return rulesRootResult{Root: in.RootOverride, RepoID: repoID(in.RootOverride), Enabled: true}
	}

	cwd := filepath.Clean(in.Cwd)
	if root, ok := findRepoRoot(cwd); ok && !isUnscannableRoot(root, in.Home) {
		return rulesRootResult{Root: root, RepoID: repoID(root), Enabled: true}
	}
	if isUnscannableRoot(cwd, in.Home) {
		return rulesRootResult{
			Enabled: false,
			Reason: "rule intelligence is off: `tokenops init` was run in " + cwd +
				", and scanning a home or root directory would walk every project on the machine. " +
				"Re-run it inside a project, or pass --rules-root <path> to choose one.",
		}
	}
	return rulesRootResult{Root: cwd, RepoID: repoID(cwd), Enabled: true}
}

// findRepoRoot walks up from dir looking for a .git entry.
func findRepoRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// isUnscannableRoot reports whether a directory is too broad to walk: the
// home directory itself, anything at or above it, or a filesystem root.
func isUnscannableRoot(dir, home string) bool {
	if dir == "" || dir == filepath.Dir(dir) {
		return true // filesystem root: Dir("/") == "/"
	}
	if home == "" {
		return false
	}
	home = filepath.Clean(home)
	if dir == home {
		return true
	}
	// A parent of home is broader still.
	if rel, err := filepath.Rel(dir, home); err == nil && rel != ".." && filepath.IsLocal(rel) {
		return true
	}
	return false
}
