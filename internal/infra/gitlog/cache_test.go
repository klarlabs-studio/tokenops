package gitlog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A directory that was not in a repository is asked about again after a
// while: a daemon that keeps a Repos would otherwise never see a
// repository created there.
func TestRootAsksAgainAboutADirectoryOutsideARepository(t *testing.T) {
	isolateGit(t)
	dir := tempDir(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	r := NewRepos()
	r.now = func() time.Time { return now }
	if got := r.Root(context.Background(), dir); got != "" {
		t.Fatalf("Root = %q before the repository exists", got)
	}
	run(t, dir, nil, "init", "-q", "-b", "main")
	now = now.Add(rootRecheck - time.Second)
	if got := r.Root(context.Background(), dir); got != "" {
		t.Errorf("Root = %q within %s of the last answer, want the answer kept", got, rootRecheck)
	}
	now = now.Add(time.Second)
	if got := r.Root(context.Background(), dir); got != dir {
		t.Errorf("Root = %q after %s, want the repository created since", got, rootRecheck)
	}
	// A root found is kept, as before.
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if got := r.Root(context.Background(), dir); got != dir {
		t.Errorf("Root = %q a day later, want the root found kept", got)
	}
}

// countGitLogs puts a git on PATH that records each git log it runs, and
// returns how many have run since.
func countGitLogs(t *testing.T) func() int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper is a shell script")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	bin := t.TempDir()
	logFile := filepath.Join(bin, "calls")
	script := "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = log ]; then echo log >> '" + logFile + "'; break; fi; done\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, err := os.ReadFile(logFile)
		if err != nil {
			return 0
		}
		return strings.Count(string(b), "log\n")
	}
}

// A history read is kept while the repository's refs and user stay as
// they were: the same weeks asked for again run no git log. Any change to
// them reads it again.
func TestHistoryIsReadAgainOnlyWhenTheRefsOrUserChange(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repo := newRepo(t, me)
	commit(t, repo, me, "feat: one (#1)", base)
	commit(t, repo, me, "feat: two (#2)", base.Add(time.Hour))
	r := NewRepos()
	ctx := context.Background()

	read := func() ([]string, []int) {
		t.Helper()
		cs, err := r.Commits(ctx, repo, base)
		if err != nil {
			t.Fatal(err)
		}
		prs, err := r.MergedPRs(ctx, repo, base)
		if err != nil {
			t.Fatal(err)
		}
		return subjects(cs), prNumbers(prs)
	}
	cs, prs := read()
	if len(cs) != 2 || len(prs) != 2 {
		t.Fatalf("read %v and %v, want two commits and two PRs", cs, prs)
	}

	// Asked again with nothing changed, the history kept answers: no git
	// log runs.
	logged := countGitLogs(t)
	gotCs, gotPRs := read()
	if !reflect.DeepEqual(gotCs, cs) || !reflect.DeepEqual(gotPRs, prs) {
		t.Errorf("unchanged repository read %v and %v, want the kept %v and %v", gotCs, gotPRs, cs, prs)
	}
	if n := logged(); n != 0 {
		t.Errorf("an unchanged repository ran git log %d times", n)
	}

	for _, change := range []struct {
		name string
		do   func()
		want int
	}{
		{"a commit", func() { commit(t, repo, me, "feat: three (#3)", base.Add(2*time.Hour)) }, 3},
		{"a branch moved back", func() { run(t, repo, nil, "reset", "-q", "--hard", "HEAD~2") }, 1},
		{"another user", func() { run(t, repo, nil, "config", "user.email", "someone@example.com") }, 0},
	} {
		change.do()
		before := logged()
		cs, err := r.Commits(ctx, repo, base)
		if err != nil {
			t.Fatal(err)
		}
		prs, err := r.MergedPRs(ctx, repo, base)
		if err != nil {
			t.Fatal(err)
		}
		if change.want > 0 && logged() == before {
			t.Errorf("after %s the history was not read again", change.name)
		}
		if len(cs) != change.want || len(prs) != change.want {
			t.Errorf("after %s: %v and %v, want %d of each", change.name, subjects(cs), prNumbers(prs), change.want)
		}
	}
}

// A later window is cut from what an earlier one read, by committer time
// as git log --since selects; an earlier one is read from git.
func TestHistoryAnswersALaterWindowFromAnEarlierRead(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repo := newRepo(t, me)
	for i := range 4 {
		commit(t, repo, me, "feat: n (#"+strconv.Itoa(i+1)+")", base.Add(time.Duration(i)*24*time.Hour))
	}
	// Authored before the window, committed in it (a rebase or a cherry
	// pick): git log --since selects it, and so must a cut.
	run(t, repo, []string{"GIT_AUTHOR_DATE=" + strconv.FormatInt(base.Add(-48*time.Hour).Unix(), 10) + " +0000",
		"GIT_COMMITTER_DATE=" + strconv.FormatInt(base.Add(80*time.Hour).Unix(), 10) + " +0000"},
		"commit", "-q", "--allow-empty", "--no-verify", "-m", "fix: picked (#9)")
	ctx := context.Background()
	r := NewRepos()
	if _, err := r.Commits(ctx, repo, base.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MergedPRs(ctx, repo, base.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// git log --since=@N selects what was committed at or after the
	// second N, so a window starting within a commit's second has it.
	for _, c := range []struct {
		since time.Duration
		want  []int
	}{
		{36 * time.Hour, []int{3, 4, 9}},
		{48*time.Hour + 500*time.Millisecond, []int{3, 4, 9}},
		{48*time.Hour + time.Second, []int{4, 9}},
		{80 * time.Hour, []int{9}},
		{24 * time.Hour, []int{2, 3, 4, 9}},
		{0, []int{1, 2, 3, 4, 9}},
	} {
		since := base.Add(c.since)
		cs, err := r.Commits(ctx, repo, since)
		if err != nil {
			t.Fatal(err)
		}
		prs, err := r.MergedPRs(ctx, repo, since)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]string, 0, len(c.want))
		for _, n := range c.want {
			if n == 9 {
				want = append(want, "fix: picked (#9)")
			} else {
				want = append(want, "feat: n (#"+strconv.Itoa(n)+")")
			}
		}
		if got := subjects(cs); !reflect.DeepEqual(got, want) {
			t.Errorf("commits since +%s: %v, want %v", c.since, got, want)
		}
		if got := prNumbers(prs); !reflect.DeepEqual(got, c.want) {
			t.Errorf("PRs since +%s: %v, want %v", c.since, got, c.want)
		}
		fresh, err := NewRepos().Commits(ctx, repo, since)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cs, fresh) {
			t.Errorf("commits since +%s cut from a kept read differ from a fresh one", c.since)
		}
	}
}

// What a caller is given is its own: changing it does not change what the
// next caller gets.
func TestHistoryHandsOutCopies(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repo := newRepo(t, me)
	commit(t, repo, me, "feat: one (#1)", base)
	r := NewRepos()
	ctx := context.Background()
	cs, _ := r.Commits(ctx, repo, base)
	prs, _ := r.MergedPRs(ctx, repo, base)
	cs[0].Subject, prs[0].Number = "changed", 99
	cs, _ = r.Commits(ctx, repo, base)
	prs, _ = r.MergedPRs(ctx, repo, base)
	if cs[0].Subject != "feat: one (#1)" || prs[0].Number != 1 {
		t.Errorf("the kept history was changed through a caller's copy: %+v, %+v", cs[0], prs[0])
	}
}

// A read cut short is not kept.
func TestHistoryDoesNotKeepAFailedRead(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	repo := newRepo(t, me)
	commit(t, repo, me, "feat: one (#1)", time.Now())
	r := NewRepos()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Commits(cancelled, repo, time.Time{}); err == nil {
		t.Fatal("a cancelled read succeeded")
	}
	cs, err := r.Commits(context.Background(), repo, time.Time{})
	if err != nil || len(cs) != 1 {
		t.Errorf("after a cancelled read: %v, %v; want the commit", subjects(cs), err)
	}
}

// A window that starts at or before the epoch is all of history. git read
// such a --since as about now, so a read from the start found nothing
// committed more than a second before.
func TestHistoryFromTheStartReadsEverything(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	hourAgo := time.Now().Add(-time.Hour)
	repo := newRepo(t, me)
	commit(t, repo, me, "feat: one (#1)", hourAgo)
	for _, since := range []time.Time{{}, time.Unix(0, 0), time.Unix(-1, 0)} {
		cs, err := NewRepos().Commits(context.Background(), repo, since)
		if err != nil || len(cs) != 1 {
			t.Errorf("commits since %v: %v, %v; want the commit an hour ago", since, subjects(cs), err)
		}
		prs, err := NewRepos().MergedPRs(context.Background(), repo, since)
		if err != nil || len(prs) != 1 {
			t.Errorf("PRs since %v: %v, %v; want the one an hour ago", since, prNumbers(prs), err)
		}
	}
}
