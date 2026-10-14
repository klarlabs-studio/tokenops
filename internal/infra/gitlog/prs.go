package gitlog

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PR is a pull request merged into a repository's default branch, as git
// records it. No forge is asked: a squash merge leaves a commit whose
// subject ends "(#N)", a merge commit says "Merge pull request #N".
type PR struct {
	Number int
	SHA    string
	// MergedAt is when the commit landed (its committer time).
	MergedAt time.Time
	Subject  string
}

var (
	squashPR = regexp.MustCompile(`\(#(\d{1,9})\)\s*$`)
	mergePR  = regexp.MustCompile(`^Merge pull request #(\d{1,9}) `)
)

// prNumberOf is the pull request a default-branch subject names, 0 for
// none.
func prNumberOf(subject string) int {
	m := mergePR.FindStringSubmatch(subject)
	if m == nil {
		m = squashPR.FindStringSubmatch(subject)
	}
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// defaultRefs are the refs of the repository's default branch: its local
// branch and its origin copy, whichever exist. The default is what
// origin/HEAD names, else main, else master.
func defaultRefs(ctx context.Context, root string) []string {
	names := []string{"main", "master"}
	if head, err := git(ctx, root, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && strings.HasPrefix(head, "origin/") {
		names = []string{strings.TrimPrefix(head, "origin/")}
	}
	for _, name := range names {
		var refs []string
		for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
			if _, err := git(ctx, root, "rev-parse", "--verify", "-q", ref); err == nil {
				refs = append(refs, ref)
			}
		}
		if len(refs) > 0 {
			return refs
		}
	}
	return nil
}

// MergedPRs are the pull requests of the repository's configured user
// merged into its default branch since since, oldest first. A squash
// merge is the user's when they authored it; a merge commit when they
// authored the tip of the branch it merged (someone else may have pressed
// the button). Only what has been fetched is seen; the forge is never
// asked.
func (r *Repos) MergedPRs(ctx context.Context, root string, since time.Time) ([]PR, error) {
	email, err := git(ctx, root, "config", "user.email")
	if err != nil || email == "" {
		return nil, nil
	}
	refs := defaultRefs(ctx, root)
	if len(refs) == 0 {
		return nil, nil
	}
	args := append([]string{"log"}, refs...)
	args = append(args, "--first-parent", "--since=@"+strconv.FormatInt(since.Unix(), 10),
		"--format=%H%x1f%ct%x1f%ae%x1f%P%x1f%s", "--")
	out, err := git(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	var prs []PR
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		n := prNumberOf(parts[4])
		if n == 0 {
			continue
		}
		at, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		author := parts[2]
		if parents := strings.Fields(parts[3]); len(parents) > 1 {
			author, _ = git(ctx, root, "log", "-1", "--format=%ae", parents[1])
		}
		if !strings.EqualFold(author, email) {
			continue
		}
		prs = append(prs, PR{Number: n, SHA: parts[0], MergedAt: time.Unix(at, 0).UTC(), Subject: parts[4]})
	}
	sort.SliceStable(prs, func(i, j int) bool { return prs[i].MergedAt.Before(prs[j].MergedAt) })
	return prs, nil
}
