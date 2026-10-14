package savings

import (
	"context"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	ftstore "go.klarlabs.de/tokenops/internal/infra/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/savingsledger"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// DefaultLedger is the ledger under the operator's home directory, nil
// when there is no home to put it in.
func DefaultLedger() Ledger {
	l, err := savingsledger.Default()
	if err != nil {
		return nil
	}
	return l
}

// DefaultDeps reads this machine's ledgers: the savings ledger, the coach
// hook's session spend, the follow-through ledger, and git history.
func DefaultDeps() Deps {
	return Deps{
		Ledger:       DefaultLedger(),
		SessionPeaks: func() map[string]float64 { return coachhook.SessionPeaks("") },
		Followed:     defaultFollowed,
		Commits:      GitCommits,
	}
}

// defaultFollowed is the follow-through ledger's verdict per offer:
// followed or not, for the offers it has resolved.
func defaultFollowed() map[string]bool {
	l, err := ftstore.Default()
	if err != nil {
		return nil
	}
	entries, err := l.Load()
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, r := range ft.Fold(entries, time.Now()) {
		switch r.Outcome {
		case ft.OutcomeFollowed, ft.OutcomeStood:
			out[r.Offer.ID] = true
		case ft.OutcomeIgnored, ft.OutcomeUndone:
			out[r.Offer.ID] = false
		}
	}
	return out
}

// commitWindow is how long after an intervention a commit still counts
// as the session's work landing.
const commitWindow = 24 * time.Hour

// GitCommits answers whether a commit landed in a session's repository
// within a day after an intervention, from the directory the client
// recorded for the session and that repository's git log. A session
// outside a repository, or a day not yet over, is unknown.
func GitCommits(ctx context.Context, since, now time.Time) func(session string, at time.Time) (bool, bool) {
	var dirs map[string]sessiondirs.Dir
	repos := gitlog.NewRepos()
	rootOf := map[string]string{}
	commitsOf := map[string][]gitlog.Commit{}
	return func(session string, at time.Time) (bool, bool) {
		if dirs == nil {
			dirs = sessiondirs.Find(sessiondirs.Roots{}, since)
		}
		d, ok := dirs[session]
		if !ok || d.CWD == "" {
			return false, false
		}
		root, seen := rootOf[d.CWD]
		if !seen {
			root = repos.Root(ctx, d.CWD)
			rootOf[d.CWD] = root
		}
		if root == "" {
			return false, false
		}
		commits, listed := commitsOf[root]
		if !listed {
			commits, _ = repos.Commits(ctx, root, since)
			commitsOf[root] = commits
		}
		for _, c := range commits {
			if c.At.After(at) && !c.At.After(at.Add(commitWindow)) {
				return true, true
			}
		}
		if now.Sub(at) < commitWindow {
			return false, false
		}
		return false, true
	}
}
