// Package gitlog reads the commits a person made in a local repository,
// for attributing agent work to them. It runs git read-only and never
// fetches.
package gitlog

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Commit is one commit, newest work last when listed.
type Commit struct {
	SHA     string
	At      time.Time
	Subject string
	Branch  string
}

// Repos resolves directories to repository roots and lists commits,
// caching both: a month of sessions names the same few repositories many
// times.
type Repos struct {
	mu    sync.Mutex
	roots map[string]string
}

// NewRepos returns an empty cache.
func NewRepos() *Repos { return &Repos{roots: map[string]string{}} }

const gitTimeout = 10 * time.Second

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed git subcommands over a session's own directory
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Root is the repository dir lies in; empty when it is not in one (or no
// longer exists).
func (r *Repos) Root(ctx context.Context, dir string) string {
	r.mu.Lock()
	if root, ok := r.roots[dir]; ok {
		r.mu.Unlock()
		return root
	}
	r.mu.Unlock()
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		root = ""
	}
	r.mu.Lock()
	r.roots[dir] = root
	r.mu.Unlock()
	return root
}

// Commits are the non-merge commits in root authored by the repository's
// configured user since since, oldest first, reachable from a local
// branch. Local branches only: --all would count git notes (provenance
// notes are commits too) and remote copies, and a branch merged by squash
// and deleted leaves its work on the squash commit — the merged change.
// No configured user means no commits: attributing someone else's work
// would be wrong.
func (r *Repos) Commits(ctx context.Context, root string, since time.Time) ([]Commit, error) {
	email, err := git(ctx, root, "config", "user.email")
	if err != nil || email == "" {
		return nil, nil
	}
	// --author matches a pattern anywhere in "Name <email>": a bare
	// address also matches jimbob@ for bob@, and its dots match any
	// character. The bracketed address as a fixed string matches the
	// operator's address whole and nothing else.
	out, err := git(ctx, root, "log", "--branches", "--no-merges", "--reverse",
		"--fixed-strings", "--author=<"+email+">",
		"--since=@"+strconv.FormatInt(since.Unix(), 10),
		"--format=%H%x1f%at%x1f%s%x1f%D")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\x1f", 4)
		if len(parts) < 3 {
			continue
		}
		at, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		c := Commit{SHA: parts[0], At: time.Unix(at, 0).UTC(), Subject: parts[2]}
		if len(parts) == 4 {
			c.Branch = branchOf(parts[3])
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// branchOf is the first local branch in a %D decoration.
func branchOf(decoration string) string {
	for _, ref := range strings.Split(decoration, ", ") {
		ref = strings.TrimPrefix(ref, "HEAD -> ")
		if ref != "" && !strings.HasPrefix(ref, "tag: ") && !strings.Contains(ref, "/") {
			return ref
		}
	}
	return ""
}
