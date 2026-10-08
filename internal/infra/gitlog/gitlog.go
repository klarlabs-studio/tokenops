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
		// A lookup cut short says nothing about the directory: answer
		// "unknown" now and ask git again next time.
		if ctx.Err() != nil {
			return ""
		}
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
		// Decorate with local branches only, so a slash-named branch
		// (feat/x) is told from a remote copy (origin/feat/x) by where it
		// lives rather than by the shape of its name.
		"--decorate-refs=refs/heads/",
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

// OriginName is "owner/name" from the repository's origin remote, or ""
// when it has none or its URL has no such shape. Host, scheme, user and
// credentials are dropped: only the two path segments are returned.
func (r *Repos) OriginName(ctx context.Context, root string) string {
	out, err := git(ctx, root, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return ownerName(out)
}

// ownerName takes the last two path segments of a remote URL, either
// scheme://host/owner/name(.git) or scp-like user@host:owner/name(.git).
func ownerName(remote string) string {
	s := strings.TrimSpace(remote)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexByte(s, '/'); j >= 0 {
			s = s[j+1:]
		} else {
			return ""
		}
	} else if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

// branchOf is the first branch in a %D decoration already limited to
// local branches. A detached HEAD or a tag names no branch.
func branchOf(decoration string) string {
	for _, ref := range strings.Split(decoration, ", ") {
		ref = strings.TrimPrefix(ref, "HEAD -> ")
		if ref != "" && ref != "HEAD" && !strings.HasPrefix(ref, "tag: ") {
			return ref
		}
	}
	return ""
}
