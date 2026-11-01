package avoidable

import (
	"sort"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// LikelyMethod says how a prompt prefix change is joined to the context
// lint, for every surface to show beside the names it gives.
const LikelyMethod = "a rebuild is joined to an item the context lint finds in the prefix every session of that harness loads " +
	"in the project the session ran in: a context file's volatile line, or a SessionStart hook's volatile output; " +
	"the lint reads the files as they are now, and no record says which byte of the prefix changed, so the item is likely, not proven"

// Linter lints the always-loaded context of the project at root, or the
// user's own files alone when root is empty.
type Linter func(root string) []prefixlint.Finding

// LikelyCause is a context-lint item that plausibly explains some of the
// prompt prefix changes: it sits in the prefix the rebuilt sessions
// loaded. It names the item, never where it is.
type LikelyCause struct {
	Finding  string `json:"finding"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Rebuilds and Sessions count the prefix-change rebuilds it was
	// joined to and the sessions they were in.
	Rebuilds int `json:"rebuilds"`
	Sessions int `json:"sessions"`
}

// likelyBook joins prefix-change rebuilds to the context lint's items.
type likelyBook struct {
	in      Inputs
	byRoot  map[string][]prefixlint.Finding
	cost    map[string]*LikelyCause
	session map[string]map[string]bool
}

func newLikelyBook(in Inputs) *likelyBook {
	return &likelyBook{in: in, byRoot: map[string][]prefixlint.Finding{}, cost: map[string]*LikelyCause{},
		session: map[string]map[string]bool{}}
}

// add joins one prefix-change rebuild in turn t to the items in the
// prefix its session loaded: the user's own files and, when the session
// ran in a repository, that repository's.
func (lb *likelyBook) add(t analytics.SessionTurn) {
	if lb.in.Lint == nil {
		return
	}
	client := allocation.HarnessOf(t.Source)
	seen := map[string]bool{}
	for _, f := range lb.findings(lb.rootOf(t.SessionID)) {
		name := f.Subject()
		if !f.InPrefix() || !f.LoadedBy(client) || seen[name] {
			continue
		}
		seen[name] = true
		c, ok := lb.cost[name]
		if !ok {
			c = &LikelyCause{Finding: name, Kind: f.Kind, Severity: f.Severity}
			lb.cost[name] = c
			lb.session[name] = map[string]bool{}
		}
		c.Rebuilds++
		lb.session[name][t.SessionID] = true
	}
}

// rootOf is the repository the session ran in; empty when its directory
// is unknown or outside a repository.
func (lb *likelyBook) rootOf(session string) string {
	d, ok := lb.in.Dirs[session]
	if !ok {
		return ""
	}
	return lb.in.RootOf[d.CWD]
}

// findings lints root once.
func (lb *likelyBook) findings(root string) []prefixlint.Finding {
	if fs, ok := lb.byRoot[root]; ok {
		return fs
	}
	fs := lb.in.Lint(root)
	lb.byRoot[root] = fs
	return fs
}

// causes are the items joined, most rebuilds first, then the most
// severe, then by name.
func (lb *likelyBook) causes() []LikelyCause {
	out := make([]LikelyCause, 0, len(lb.cost))
	for name, c := range lb.cost {
		c.Sessions = len(lb.session[name])
		out = append(out, *c)
	}
	rank := map[string]int{prefixlint.SeverityHigh: 0, prefixlint.SeverityMedium: 1, prefixlint.SeverityLow: 2}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rebuilds != b.Rebuilds {
			return a.Rebuilds > b.Rebuilds
		}
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		return a.Finding < b.Finding
	})
	return out
}

// attach puts the joined items on the prefix-change cause, when any.
func (lb *likelyBook) attach(causes []CauseCost) {
	likely := lb.causes()
	if len(likely) == 0 {
		return
	}
	for i := range causes {
		if causes[i].Cause == waste.CausePrefixChanged {
			causes[i].Likely = likely
			causes[i].LikelyMethod = LikelyMethod
		}
	}
}
