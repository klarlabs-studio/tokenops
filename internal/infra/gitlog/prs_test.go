package gitlog

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func prNumbers(prs []PR) []int {
	out := make([]int, 0, len(prs))
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out
}

// A merged pull request is what landed on the default branch: a squash
// commit whose subject ends "(#N)" or a "Merge pull request #N" merge
// commit. Only the operator's are counted, the remote copy of the default
// branch counts as well as the local one, and work merged into some other
// branch is not a merged pull request.
func TestMergedPRsReadsSquashAndMergeCommitsOnTheDefaultBranch(t *testing.T) {
	isolateGit(t)
	const me = "me@example.com"
	const them = "colleague@example.com"
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	since := base.Add(24 * time.Hour)
	repo := newRepo(t, me)

	commit(t, repo, me, "feat: before the window (#1)", base)
	commit(t, repo, me, "feat(spend): squash merged (#12)", since.Add(time.Hour))
	commit(t, repo, them, "fix: a colleague's squash (#14)", since.Add(2*time.Hour))
	commit(t, repo, me, "chore: a direct commit, no PR", since.Add(3*time.Hour))

	// A merge commit made by someone else for the operator's branch is
	// the operator's pull request.
	run(t, repo, nil, "checkout", "-q", "-b", "me/side")
	commit(t, repo, me, "work on side", since.Add(4*time.Hour))
	run(t, repo, nil, "checkout", "-q", "main")
	stamp := strconv.FormatInt(since.Add(5*time.Hour).Unix(), 10) + " +0000"
	run(t, repo, []string{"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp, "GIT_AUTHOR_EMAIL=" + them, "GIT_COMMITTER_EMAIL=" + them},
		"merge", "-q", "--no-ff", "-m", "Merge pull request #13 from me/side", "me/side")

	// A colleague's branch merged the same way is theirs.
	run(t, repo, nil, "checkout", "-q", "-b", "them/side")
	commit(t, repo, them, "their work", since.Add(6*time.Hour))
	run(t, repo, nil, "checkout", "-q", "main")
	stamp = strconv.FormatInt(since.Add(7*time.Hour).Unix(), 10) + " +0000"
	run(t, repo, []string{"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp},
		"merge", "-q", "--no-ff", "-m", "Merge pull request #15 from them/side", "them/side")

	// A "(#N)" subject on a feature branch has not landed.
	run(t, repo, nil, "checkout", "-q", "-b", "feat/x")
	commit(t, repo, me, "feat: not merged yet (#98)", since.Add(8*time.Hour))
	run(t, repo, nil, "checkout", "-q", "main")

	// The remote default branch is ahead of the local one: merged on the
	// forge, fetched, not pulled.
	run(t, repo, nil, "update-ref", "refs/remotes/origin/main", "HEAD")
	run(t, repo, nil, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	run(t, repo, nil, "checkout", "-q", "--detach", "origin/main")
	commit(t, repo, me, "refactor: merged on the forge (#16)", since.Add(9*time.Hour))
	run(t, repo, nil, "update-ref", "refs/remotes/origin/main", "HEAD")
	run(t, repo, nil, "checkout", "-q", "main")

	got, err := NewRepos().MergedPRs(context.Background(), repo, since)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{12, 13, 16}
	if g := prNumbers(got); len(g) != len(want) || g[0] != 12 || g[1] != 13 || g[2] != 16 {
		t.Fatalf("PRs = %v, want %v (oldest first)", g, want)
	}
	if got[0].MergedAt != since.Add(time.Hour) || got[0].Subject != "feat(spend): squash merged (#12)" {
		t.Errorf("first PR = %+v", got[0])
	}
	if got[1].MergedAt != since.Add(5*time.Hour) {
		t.Errorf("merge commit merged at %v, want %v", got[1].MergedAt, since.Add(5*time.Hour))
	}
}

// Without a default branch there is nothing merged; without a configured
// user there is no one to attribute a pull request to.
func TestMergedPRsWithoutDefaultBranchOrUserIsEmpty(t *testing.T) {
	isolateGit(t)
	noUser := newRepo(t, "")
	commit(t, noUser, "someone@example.com", "feat: x (#1)", time.Now())
	if got, err := NewRepos().MergedPRs(context.Background(), noUser, time.Time{}); err != nil || got != nil {
		t.Errorf("no user: %v, %v; want nil, nil", got, err)
	}

	const me = "me@example.com"
	trunk := newRepo(t, me)
	run(t, trunk, nil, "checkout", "-q", "-b", "trunk")
	commit(t, trunk, me, "feat: x (#1)", time.Now())
	if got, err := NewRepos().MergedPRs(context.Background(), trunk, time.Time{}); err != nil || got != nil {
		t.Errorf("no main or master: %v, %v; want nil, nil", got, err)
	}
}

func TestPRNumberOf(t *testing.T) {
	for subject, want := range map[string]int{
		"feat(spend): FOCUS export (#667)":               667,
		"fix: x (#12) ":                                  12,
		"Merge pull request #13 from me/side":            13,
		"Merge pull request #7 from org/repo/branch":     7,
		"feat: mentions #12 in the middle":               0,
		"fix: (#12) not at the end":                      0,
		"feat: x (#)":                                    0,
		"Merge branch 'main' into feat/x":                0,
		"revert: feat: x (#5) (#9)":                      9,
		"feat: very long number (#99999999999999999999)": 0,
	} {
		if got := prNumberOf(subject); got != want {
			t.Errorf("prNumberOf(%q) = %d, want %d", subject, got, want)
		}
	}
}
