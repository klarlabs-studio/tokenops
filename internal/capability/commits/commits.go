// Package commits answers what each commit cost: the agent work that led
// to it, priced. A turn belongs to the repository its session ran in, and
// to the first commit its author made there at or after it, within a day.
// That is a heuristic — the work and the commit are joined by place and
// time, not by a recorded link — and the report says so, and keeps what it
// could not place apart rather than dropping it.
package commits

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// MaxGap is how long after a turn a commit may come and still be its
// result. Work left longer than a day before a commit is reported apart.
const MaxGap = 24 * time.Hour

// Method says how turns were joined to commits, for every surface to show.
const Method = "time window: each turn goes to the next commit its author made in the same repository within 24 hours"

// Cost is what some work cost.
type Cost struct {
	Turns  int   `json:"turns"`
	Tokens int64 `json:"tokens"`
	// APIEquivalentUSD is the work at list prices, its value where a plan
	// covers it; CostUSD what was billed.
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	CostUSD          float64 `json:"cost_usd"`
	// UnpricedTurns used models with no list price; the money leaves them
	// out.
	UnpricedTurns int `json:"unpriced_turns,omitempty"`
}

// Add counts one turn into c.
func (c *Cost) Add(t analytics.SessionTurn) {
	c.Turns++
	c.Tokens += t.Tokens
	c.APIEquivalentUSD += t.APIEquivalentUSD
	c.CostUSD += t.CostUSD
	if !t.Priced {
		c.UnpricedTurns++
	}
}

// CommitCost is one commit and the work that led to it.
type CommitCost struct {
	Repo    string    `json:"repo"`
	SHA     string    `json:"sha"`
	At      time.Time `json:"at"`
	Subject string    `json:"subject,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Cost
	Sessions int `json:"sessions"`
	// WorkMinutes is from the first turn to the commit.
	WorkMinutes float64 `json:"work_minutes"`
}

// Report is the cost of each commit in a window, and of the work that
// could not be placed on one.
type Report struct {
	Since   time.Time    `json:"since"`
	Method  string       `json:"method"`
	Commits []CommitCost `json:"commits"`
	// CommitsTotal is how many commits the window has; Commits may list
	// only the most recent (see Newest).
	CommitsTotal int `json:"commits_total"`
	// MedianUSD and MeanUSD are per commit, at API-equivalent prices.
	MedianUSD float64 `json:"median_usd"`
	MeanUSD   float64 `json:"mean_usd"`
	// Uncommitted is work after a repository's last commit: in progress
	// or abandoned.
	Uncommitted Cost `json:"uncommitted"`
	// NoCommitWithinADay is work no commit followed within MaxGap.
	NoCommitWithinADay Cost `json:"no_commit_within_a_day"`
	// OutsideARepository ran in a directory that is not a repository.
	OutsideARepository Cost `json:"outside_a_repository"`
	// UnknownDirectory is from sessions whose directory no client
	// recorded (or whose transcript is gone).
	UnknownDirectory Cost `json:"unknown_directory"`
	// AttributedShare is the share of the window's API-equivalent value
	// placed on a commit.
	AttributedShare float64 `json:"attributed_share"`
	// SubjectsWithheld is set where commit subjects are left out.
	SubjectsWithheld bool `json:"subjects_withheld,omitempty"`
}

// Deps are the readers the report draws on.
type Deps struct {
	Turns func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)
	// Dirs maps sessions to where they ran; nil reads the clients'
	// defaults.
	Dirs  func(since time.Time) map[string]sessiondirs.Dir
	Repos *gitlog.Repos
}

// Compute builds the report for the work since since.
func Compute(ctx context.Context, d Deps, since time.Time) (Report, error) {
	w, err := Gather(ctx, d, since)
	if err != nil {
		return Report{}, err
	}
	return Attribute(since, w.Turns, w.Dirs, w.RootOf, w.CommitsOf), nil
}

// Work is what the join reads: the turns since a time, where each
// session ran, each directory's repository, and each repository's
// commits.
type Work struct {
	Turns     []analytics.SessionTurn
	Dirs      map[string]sessiondirs.Dir
	RootOf    map[string]string
	CommitsOf map[string][]gitlog.Commit
	// Repos is the cache the roots were resolved through, for a caller
	// that reads more of each repository.
	Repos *gitlog.Repos
}

// Gather reads the work since since: the impure half of Compute.
func Gather(ctx context.Context, d Deps, since time.Time) (Work, error) {
	turns, err := d.Turns(ctx, since)
	if err != nil {
		return Work{}, err
	}
	dirsOf := d.Dirs
	if dirsOf == nil {
		dirsOf = func(s time.Time) map[string]sessiondirs.Dir { return sessiondirs.Find(sessiondirs.Roots{}, s) }
	}
	repos := d.Repos
	if repos == nil {
		repos = gitlog.NewRepos()
	}
	w := Work{Turns: turns, Dirs: dirsOf(since), RootOf: map[string]string{}, CommitsOf: map[string][]gitlog.Commit{}, Repos: repos}
	for _, dir := range w.Dirs {
		if _, seen := w.RootOf[dir.CWD]; seen {
			continue
		}
		root := repos.Root(ctx, dir.CWD)
		w.RootOf[dir.CWD] = root
		if _, listed := w.CommitsOf[root]; root != "" && !listed {
			w.CommitsOf[root], _ = repos.Commits(ctx, root, since)
		}
	}
	return w, nil
}

// Roots are the repositories the work ran in, sorted.
func (w Work) Roots() []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range w.RootOf {
		if root != "" && !seen[root] {
			seen[root] = true
			out = append(out, root)
		}
	}
	sort.Strings(out)
	return out
}

// Placement is where the join put one turn.
type Placement int

// The placements: on a commit, or apart, by why.
const (
	// OnCommit is a turn its author's next commit in the repository
	// followed within MaxGap.
	OnCommit Placement = iota
	// Uncommitted is a turn after the repository's last commit: in
	// progress or abandoned.
	Uncommitted
	// NoCommitWithinADay is a turn no commit followed within MaxGap.
	NoCommitWithinADay
	// OutsideARepository ran in a directory that is not a repository.
	OutsideARepository
	// UnknownDirectory is from a session whose directory no client
	// recorded.
	UnknownDirectory
)

// Place joins one turn to a commit: the repository its session ran in,
// and the first commit there at or after it, within MaxGap. The commit is
// set only when the placement is OnCommit.
func Place(t analytics.SessionTurn, dirs map[string]sessiondirs.Dir, rootOf map[string]string,
	commitsOf map[string][]gitlog.Commit) (Placement, gitlog.Commit) {
	dir, ok := dirs[t.SessionID]
	if !ok {
		return UnknownDirectory, gitlog.Commit{}
	}
	root := rootOf[dir.CWD]
	if root == "" {
		return OutsideARepository, gitlog.Commit{}
	}
	commits := commitsOf[root]
	i := sort.Search(len(commits), func(i int) bool { return !commits[i].At.Before(t.At) })
	switch {
	case i == len(commits):
		return Uncommitted, gitlog.Commit{}
	case commits[i].At.Sub(t.At) > MaxGap:
		return NoCommitWithinADay, gitlog.Commit{}
	}
	return OnCommit, commits[i]
}

// Attribute places each turn on a commit: the pure half of Compute.
func Attribute(since time.Time, turns []analytics.SessionTurn, dirs map[string]sessiondirs.Dir,
	rootOf map[string]string, commitsOf map[string][]gitlog.Commit) Report {
	r := Report{Since: since, Method: Method, Commits: []CommitCost{}}
	type key struct{ root, sha string }
	byCommit := map[key]*CommitCost{}
	sessions := map[key]map[string]bool{}
	first := map[key]time.Time{}
	var total float64
	for _, t := range turns {
		total += t.APIEquivalentUSD
		placed, c := Place(t, dirs, rootOf, commitsOf)
		switch placed {
		case UnknownDirectory:
			r.UnknownDirectory.Add(t)
			continue
		case OutsideARepository:
			r.OutsideARepository.Add(t)
			continue
		case Uncommitted:
			r.Uncommitted.Add(t)
			continue
		case NoCommitWithinADay:
			r.NoCommitWithinADay.Add(t)
			continue
		}
		root := rootOf[dirs[t.SessionID].CWD]
		k := key{root, c.SHA}
		cc := byCommit[k]
		if cc == nil {
			cc = &CommitCost{Repo: filepath.Base(root), SHA: c.SHA[:min(12, len(c.SHA))], At: c.At, Subject: c.Subject, Branch: c.Branch}
			byCommit[k] = cc
			sessions[k] = map[string]bool{}
			first[k] = t.At
		}
		cc.Add(t)
		sessions[k][t.SessionID] = true
		if t.At.Before(first[k]) {
			first[k] = t.At
		}
	}
	var attributed float64
	values := make([]float64, 0, len(byCommit))
	for k, cc := range byCommit {
		cc.Sessions = len(sessions[k])
		cc.WorkMinutes = cc.At.Sub(first[k]).Minutes()
		r.Commits = append(r.Commits, *cc)
		attributed += cc.APIEquivalentUSD
		values = append(values, cc.APIEquivalentUSD)
	}
	sort.Slice(r.Commits, func(i, j int) bool { return r.Commits[i].At.After(r.Commits[j].At) })
	r.CommitsTotal = len(r.Commits)
	if len(values) > 0 {
		sort.Float64s(values)
		r.MedianUSD = values[len(values)/2]
		if len(values)%2 == 0 {
			r.MedianUSD = (values[len(values)/2-1] + values[len(values)/2]) / 2
		}
		r.MeanUSD = attributed / float64(len(values))
	}
	if total > 0 {
		r.AttributedShare = attributed / total
	}
	return r
}

// Newest is r listing only its n most recent commits; the totals and
// figures still cover them all.
func (r Report) Newest(n int) Report {
	if n <= 0 || len(r.Commits) <= n {
		return r
	}
	out := r
	out.Commits = r.Commits[:n]
	return out
}

// WithoutSubjects is r with commit subjects removed, as the daemon API
// serves it: a subject is the repository's own text, not a figure.
func (r Report) WithoutSubjects() Report {
	out := r
	out.Commits = make([]CommitCost, len(r.Commits))
	for i, c := range r.Commits {
		c.Subject = ""
		out.Commits[i] = c
	}
	out.SubjectsWithheld = true
	return out
}
