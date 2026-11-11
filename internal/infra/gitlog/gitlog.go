// Package gitlog reads the commits a person made in a local repository,
// for attributing agent work to them. It runs git read-only and never
// fetches.
package gitlog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoAuthor is a repository with no user.email configured: its
// commits cannot be told from anyone else's, so none are listed. It is an
// error rather than an empty list because "no commits of yours" would
// judge every turn there unfinished.
var ErrNoAuthor = errors.New("gitlog: no user.email configured")

// Commit is one commit, newest work last when listed.
type Commit struct {
	SHA     string
	At      time.Time
	Subject string
	Branch  string
	// Reverts is the commit this one undoes, as its body names it
	// ("This reverts commit <sha>."), full or abbreviated; empty for a
	// commit that is not a revert.
	Reverts string
}

// revertedSHA reads the commit a revert's body names.
var revertedSHA = regexp.MustCompile(`This reverts commit ([0-9a-f]{7,40})`)

// Repos resolves directories to repository roots and lists commits and
// merged pull requests, caching them: a month of sessions names the same
// few repositories many times, and a process that keeps a Repos asks for
// the same weeks of history request after request.
//
// A directory's root is kept for good, but "not in a repository" only
// for rootRecheck: a directory may become one. Commits and pull requests
// are kept with the user and the refs they were read at, and read again
// when either changed.
//
// It is safe for concurrent use.
type Repos struct {
	mu    sync.Mutex
	roots map[string]rootLookup
	// commits and prs are each repository's history as last read.
	commits map[string]*history[Commit]
	prs     map[string]*history[PR]
	now     func() time.Time
}

// rootLookup is what git answered for a directory, and when.
type rootLookup struct {
	root string
	at   time.Time
}

// rootRecheck is how long "not in a repository" stands before git is
// asked again.
const rootRecheck = 10 * time.Minute

// NewRepos returns an empty cache.
func NewRepos() *Repos {
	return &Repos{roots: map[string]rootLookup{}, commits: map[string]*history[Commit]{},
		prs: map[string]*history[PR]{}, now: time.Now}
}

// history is one repository's commits or merged pull requests as last
// read, oldest first.
type history[T any] struct {
	// state is the user they were read for and the refs they were read
	// at: the same state holds the same history.
	state string
	// since is how far back they were read.
	since time.Time
	items []T
	// committed is each item's committer time, which git's --since
	// selects by.
	committed []time.Time
}

// answers reports whether h holds what a read at state from since gives.
func (h *history[T]) answers(state string, since time.Time) bool {
	return h != nil && h.state == state && !since.Before(h.since)
}

// from appends to out, a new slice, the items committed at or after
// since: what git log --since gives, to the second.
func (h *history[T]) from(since time.Time, out []T) []T {
	floor := time.Unix(since.Unix(), 0)
	for i, at := range h.committed {
		if !at.Before(floor) {
			out = append(out, h.items[i])
		}
	}
	return out
}

// sinceArgs selects what was committed at or after since. git does not
// read a time at or before the epoch as the distant past: @0 and earlier
// fall back to about now, and selected nothing older than a second. A
// window that starts there is all of history, which no --since reads.
func sinceArgs(since time.Time) []string {
	if since.Unix() <= 0 {
		return nil
	}
	return []string{"--since=@" + strconv.FormatInt(since.Unix(), 10)}
}

// refState is what a repository's history is read at: every ref and what
// it points to, hashed. A commit, a fetch, a merge, a rebase or a branch
// deleted changes it.
func refState(ctx context.Context, root, email string) (string, error) {
	out, err := git(ctx, root, "for-each-ref", "--format=%(refname) %(objectname) %(symref)")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(email + "\x00" + out))
	return hex.EncodeToString(sum[:]), nil
}

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
	if l, ok := r.roots[dir]; ok && (l.root != "" || r.now().Sub(l.at) < rootRecheck) {
		r.mu.Unlock()
		return l.root
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
	r.roots[dir] = rootLookup{root: root, at: r.now()}
	r.mu.Unlock()
	return root
}

// Commits are the non-merge commits in root authored by the repository's
// configured user since since, oldest first, reachable from a local
// branch. Local branches only: --all would count git notes (provenance
// notes are commits too) and remote copies, and a branch merged by squash
// and deleted leaves its work on the squash commit — the merged change.
// No configured user is ErrNoAuthor: attributing someone else's work
// would be wrong, and so would answering that there is none.
func (r *Repos) Commits(ctx context.Context, root string, since time.Time) ([]Commit, error) {
	email, err := git(ctx, root, "config", "user.email")
	if err != nil || email == "" {
		return nil, ErrNoAuthor
	}
	// The refs are read before the history: a commit landing between the
	// two leaves a state that no longer holds, and the next ask reads
	// again.
	state, stateErr := refState(ctx, root, email)
	if stateErr == nil {
		r.mu.Lock()
		if h := r.commits[root]; h.answers(state, since) {
			out := h.from(since, []Commit{})
			r.mu.Unlock()
			return out, nil
		}
		r.mu.Unlock()
	}
	h, err := readCommits(ctx, root, email, since)
	if err != nil {
		return nil, err
	}
	if stateErr == nil {
		h.state = state
		r.mu.Lock()
		r.commits[root] = h
		r.mu.Unlock()
	}
	return h.from(since, []Commit{}), nil
}

// readCommits runs git log for Commits.
func readCommits(ctx context.Context, root, email string, since time.Time) (*history[Commit], error) {
	// --author matches a pattern anywhere in "Name <email>": a bare
	// address also matches jimbob@ for bob@, and its dots match any
	// character. The bracketed address as a fixed string matches the
	// operator's address whole and nothing else.
	args := append([]string{"log", "--branches", "--no-merges", "--reverse",
		"--fixed-strings", "--author=<" + email + ">"}, sinceArgs(since)...)
	out, err := git(ctx, root, append(args,
		// Decorate with local branches only, so a slash-named branch
		// (feat/x) is told from a remote copy (origin/feat/x) by where it
		// lives rather than by the shape of its name.
		"--decorate-refs=refs/heads/",
		// The body can span lines, so records end in a record separator.
		"--format=%H%x1f%at%x1f%ct%x1f%s%x1f%D%x1f%b%x1e")...)
	if err != nil {
		return nil, err
	}
	h := &history[Commit]{since: since}
	for _, record := range strings.Split(out, "\x1e") {
		parts := strings.SplitN(strings.TrimLeft(record, "\n"), "\x1f", 6)
		if len(parts) < 4 {
			continue
		}
		at, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		committed, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		c := Commit{SHA: parts[0], At: time.Unix(at, 0).UTC(), Subject: parts[3]}
		if len(parts) >= 5 {
			c.Branch = branchOf(parts[4])
		}
		if len(parts) == 6 {
			if m := revertedSHA.FindStringSubmatch(parts[5]); m != nil {
				c.Reverts = m[1]
			}
		}
		h.items = append(h.items, c)
		h.committed = append(h.committed, time.Unix(committed, 0))
	}
	return h, nil
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
