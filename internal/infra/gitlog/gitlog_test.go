package gitlog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// isolateGit points git at an empty global config and no system config,
// so the operator's own ~/.gitconfig (user.email, hooks, signing) never
// leaks into a test and the test never writes outside its temp dir.
func isolateGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	global := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

// tempDir is t.TempDir with symlinks resolved, because git reports the
// real path (/private/var on macOS) for --show-toplevel.
func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func run(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo initialises a repository on branch main, with user.email set
// to email when it is not empty.
func newRepo(t *testing.T, email string) string {
	t.Helper()
	dir := tempDir(t)
	run(t, dir, nil, "init", "-q", "-b", "main")
	run(t, dir, nil, "config", "commit.gpgsign", "false")
	if email != "" {
		run(t, dir, nil, "config", "user.email", email)
		run(t, dir, nil, "config", "user.name", "Operator")
	}
	return dir
}

// commit makes an empty commit at at, authored by email.
func commit(t *testing.T, dir, email, subject string, at time.Time) {
	t.Helper()
	stamp := strconv.FormatInt(at.Unix(), 10) + " +0000"
	run(t, dir, []string{
		"GIT_AUTHOR_NAME=Someone", "GIT_AUTHOR_EMAIL=" + email, "GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_NAME=Someone", "GIT_COMMITTER_EMAIL=" + email, "GIT_COMMITTER_DATE=" + stamp,
	}, "commit", "-q", "--allow-empty", "--no-verify", "-m", subject)
}

func subjects(cs []Commit) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Subject)
	}
	return out
}

func TestRootResolvesRepositoryTopLevel(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t, "me@example.com")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	plain := tempDir(t)

	tests := []struct {
		name string
		dir  string
		want string
	}{
		{"repository root", repo, repo},
		{"nested directory", sub, repo},
		{"directory outside any repository", plain, ""},
		{"directory that no longer exists", filepath.Join(plain, "gone"), ""},
	}
	r := NewRepos()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Root(context.Background(), tt.dir); got != tt.want {
				t.Errorf("Root(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}

// A month of sessions names the same directory many times; the second
// lookup must not run git again, so it answers even once the repository
// is gone.
func TestRootIsCachedPerDirectory(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t, "me@example.com")
	r := NewRepos()
	if got := r.Root(context.Background(), repo); got != repo {
		t.Fatalf("first Root = %q, want %q", got, repo)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := r.Root(context.Background(), repo); got != repo {
		t.Errorf("cached Root = %q, want %q", got, repo)
	}
	if got := NewRepos().Root(context.Background(), repo); got != "" {
		t.Errorf("fresh cache Root = %q, want empty once .git is gone", got)
	}
}

// A lookup cut short by its context says nothing about the directory, so
// it must not be remembered as "not a repository".
func TestRootDoesNotCacheACancelledLookup(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t, "me@example.com")
	r := NewRepos()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := r.Root(cancelled, repo); got != "" {
		t.Fatalf("cancelled Root = %q, want empty", got)
	}
	if got := r.Root(context.Background(), repo); got != repo {
		t.Errorf("Root after a cancelled lookup = %q, want %q", got, repo)
	}
}

// Without a configured user there is no one to attribute work to, and
// guessing would credit someone else's commits.
func TestCommitsWithoutConfiguredUserIsEmpty(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t, "")
	commit(t, repo, "someone@example.com", "theirs", time.Now())
	got, err := NewRepos().Commits(context.Background(), repo, time.Time{})
	if err != nil || got != nil {
		t.Errorf("Commits = %v, %v; want nil, nil", got, err)
	}
}

func TestCommitsOutsideRepositoryIsEmpty(t *testing.T) {
	isolateGit(t)
	// No repository means no configured user is found either: nothing
	// to attribute, and no error to surface.
	got, err := NewRepos().Commits(context.Background(), tempDir(t), time.Time{})
	if err != nil || got != nil {
		t.Errorf("Commits = %v, %v; want nil, nil", got, err)
	}
}

// A user configured globally but a directory that is not a repository:
// config resolves, log fails, and the failure is reported.
func TestCommitsReportsLogFailure(t *testing.T) {
	isolateGit(t)
	run(t, tempDir(t), nil, "config", "--global", "user.email", "me@example.com")
	_, err := NewRepos().Commits(context.Background(), tempDir(t), time.Time{})
	if err == nil {
		t.Error("Commits outside a repository with a global user: want an error from git log")
	}
}

func TestCommitsSelectsTheOperatorsRecentLocalWork(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	since := base.Add(24 * time.Hour)
	repo := newRepo(t, me)

	commit(t, repo, me, "before the window", base)
	commit(t, repo, me, "first in window", since.Add(time.Hour))
	commit(t, repo, "colleague@example.com", "a colleague's", since.Add(2*time.Hour))
	commit(t, repo, me, "second in window", since.Add(3*time.Hour))

	// Work on a side branch that is merged back: the work counts, the
	// merge commit does not.
	run(t, repo, nil, "checkout", "-q", "-b", "side")
	commit(t, repo, me, "on side", since.Add(4*time.Hour))
	run(t, repo, nil, "checkout", "-q", "main")
	commit(t, repo, me, "on main", since.Add(5*time.Hour))
	stamp := strconv.FormatInt(since.Add(6*time.Hour).Unix(), 10) + " +0000"
	run(t, repo, []string{"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp},
		"merge", "-q", "--no-ff", "--no-edit", "side")
	run(t, repo, nil, "branch", "-q", "-D", "side")

	// A commit reachable only from a remote-tracking ref is someone's
	// copy, not local work.
	run(t, repo, nil, "checkout", "-q", "-b", "gone")
	commit(t, repo, me, "remote only", since.Add(7*time.Hour))
	run(t, repo, nil, "update-ref", "refs/remotes/origin/gone", "HEAD")
	run(t, repo, nil, "checkout", "-q", "main")
	run(t, repo, nil, "branch", "-q", "-D", "gone")

	// Git notes are commits too, authored by the operator, under
	// refs/notes — never work.
	run(t, repo, nil, "notes", "add", "-m", "provenance", "HEAD")

	got, err := NewRepos().Commits(context.Background(), repo, since)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first in window", "second in window", "on side", "on main"}
	if strings.Join(subjects(got), "|") != strings.Join(want, "|") {
		t.Fatalf("subjects = %q, want %q (oldest first)", subjects(got), want)
	}
	for i, c := range got {
		if len(c.SHA) != 40 {
			t.Errorf("commit %d SHA %q, want a full hash", i, c.SHA)
		}
		if c.At.Location() != time.UTC || c.At.Before(since) {
			t.Errorf("commit %d At = %v, want UTC and within the window", i, c.At)
		}
	}
	if got[0].At != since.Add(time.Hour) {
		t.Errorf("first At = %v, want %v", got[0].At, since.Add(time.Hour))
	}
}

// Author matching is the operator's address, whole: a longer address
// that merely contains it, or one that only matches it as a regular
// expression, belongs to someone else.
func TestCommitsMatchesTheAuthorAddressExactly(t *testing.T) {
	isolateGit(t)
	const me = "bob.s@example.com"
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	repo := newRepo(t, me)
	commit(t, repo, me, "mine", since.Add(time.Hour))
	commit(t, repo, "jim"+me, "contains my address", since.Add(2*time.Hour))
	commit(t, repo, "bobXs@example.com", "matches as a regexp", since.Add(3*time.Hour))
	commit(t, repo, me+".au", "my address as a prefix", since.Add(4*time.Hour))

	got, err := NewRepos().Commits(context.Background(), repo, since)
	if err != nil {
		t.Fatal(err)
	}
	if s := subjects(got); len(s) != 1 || s[0] != "mine" {
		t.Errorf("subjects = %q, want only the operator's own commit", s)
	}
}

// The branch a commit names is the local branch at its tip — including
// the slash-named feat/… and fix/… branches most repositories use — and
// never a remote copy of it or a tag.
func TestCommitsNamesTheLocalBranchAtTheTip(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	repo := newRepo(t, me)
	commit(t, repo, me, "base", since.Add(time.Hour))
	run(t, repo, nil, "tag", "v1")
	run(t, repo, nil, "update-ref", "refs/remotes/origin/main", "HEAD")
	run(t, repo, nil, "checkout", "-q", "-b", "feat/attribution")
	commit(t, repo, me, "feature tip", since.Add(2*time.Hour))
	run(t, repo, nil, "update-ref", "refs/remotes/origin/feat/attribution", "HEAD")
	commit(t, repo, me, "pushed copy is behind", since.Add(3*time.Hour))
	run(t, repo, nil, "checkout", "-q", "main")

	got, err := NewRepos().Commits(context.Background(), repo, since)
	if err != nil {
		t.Fatal(err)
	}
	branches := map[string]string{}
	for _, c := range got {
		branches[c.Subject] = c.Branch
	}
	want := map[string]string{"base": "main", "feature tip": "", "pushed copy is behind": "feat/attribution"}
	for subject, branch := range want {
		if b, ok := branches[subject]; !ok || b != branch {
			t.Errorf("%q branch = %q, want %q (all: %v)", subject, b, branch, branches)
		}
	}
}

func TestBranchOf(t *testing.T) {
	tests := []struct {
		decoration, want string
	}{
		{"", ""},
		{"HEAD -> main", "main"},
		{"HEAD -> main, origin/main, origin/HEAD", "main"},
		{"tag: v1.2.0, main", "main"},
		{"tag: v1.2.0", ""},
		{"HEAD", ""},
		{"HEAD -> feat/x", "feat/x"},
		{"feat/x, fix/y", "feat/x"},
	}
	for _, tt := range tests {
		t.Run(tt.decoration, func(t *testing.T) {
			if got := branchOf(tt.decoration); got != tt.want {
				t.Errorf("branchOf(%q) = %q, want %q", tt.decoration, got, tt.want)
			}
		})
	}
}

// A cancelled caller stops git rather than waiting it out.
func TestCommitsHonoursCancellation(t *testing.T) {
	isolateGit(t)
	repo := newRepo(t, "me@example.com")
	commit(t, repo, "me@example.com", "x", time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := NewRepos().Commits(ctx, repo, time.Time{})
	if got != nil {
		t.Errorf("Commits after cancel = %v (err %v), want nothing", got, err)
	}
}

// Only owner/name leaves a remote URL: no host, scheme or credentials.
func TestOwnerName(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/klarlabs/tokenops.git":         "klarlabs/tokenops",
		"https://user:secret@gitlab.example.eu/a/b/c/repo": "c/repo",
		"git@github.com:klarlabs/tokenops.git":             "klarlabs/tokenops",
		"ssh://git@host:2222/team/svc/":                    "team/svc",
		"https://github.com":                               "",
		"":                                                 "",
	} {
		if got := ownerName(in); got != want {
			t.Errorf("ownerName(%q) = %q, want %q", in, got, want)
		}
	}
}
