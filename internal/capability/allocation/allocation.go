// Package allocation answers what each project, client and cost centre
// spent: it tags every session's turns by the operator's allocation rules
// (internal/contexts/spend/allocation) and sums them per tag.
//
// A session is matched by where it ran — the working directory its client
// recorded, the repository that directory is in, the branch the client
// recorded — and by the harness that recorded it. The rules and the tags
// they give stay on this machine: no upload to a team plane carries them.
package allocation

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	dom "go.klarlabs.de/tokenops/internal/contexts/spend/allocation"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// The domain's vocabulary, for adapters.
type (
	// Rule tags the work it matches.
	Rule = dom.Rule
	// Tags are a session's finalized values.
	Tags = dom.Tags
	// Key is project, client or cost_center.
	Key = dom.Key
	// Match is what a rule matches work by.
	Match = dom.Match
	// Scope is one key=value.
	Scope = dom.Scope
)

// The allocation keys.
const (
	Project    = dom.Project
	Client     = dom.Client
	CostCenter = dom.CostCenter
)

// Keys lists every key.
var Keys = dom.Keys

// ParseKey reads a key.
func ParseKey(s string) (Key, error) { return dom.ParseKey(s) }

// ParseScope reads "key=value".
func ParseScope(s string) (Scope, error) { return dom.ParseScope(s) }

// Method says how work is tagged, for every surface to show.
const Method = "each session is matched by the directory and branch its client recorded, the repository that directory is in, and the harness; per key, the first rule that matches and sets it gives the value"

// harnesses names the harness each reader's turns ran in, by the prefix
// of its source tag.
var harnesses = []struct{ prefix, harness string }{
	{"claude-code", "claude-code"},
	{"codex", "codex"},
	{"opencode", "opencode"},
	{"cursor", "cursor"},
	{"gemini-cli", "gemini-cli"},
	{"pi-sessions", "pi"},
	{"github-copilot", "copilot"},
}

// HarnessOf names the harness a turn recorded by source ran in; a source
// no harness is known for names itself.
func HarnessOf(source string) string {
	for _, h := range harnesses {
		if strings.HasPrefix(source, h.prefix) {
			return h.harness
		}
	}
	return source
}

// Deps are the readers tagging draws on.
type Deps struct {
	// Turns reads the session turns since a time.
	Turns func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)
	// Dirs maps sessions to where they ran; nil reads the clients'
	// defaults.
	Dirs func(since time.Time) map[string]sessiondirs.Dir
	// Repos resolves repositories; nil makes a fresh cache.
	Repos *gitlog.Repos
	Rules []Rule
}

// TaggedTurn is a turn and its session's tags.
type TaggedTurn struct {
	analytics.SessionTurn
	Tags Tags
	// DirKnown is false when no client recorded where the session ran,
	// so only a harness rule could match it.
	DirKnown bool
}

// Place is where sessions ran: each session's directory, each directory's
// repository root, and the names each root goes by.
type Place struct {
	Dirs   map[string]sessiondirs.Dir
	RootOf map[string]string
	Names  map[string][]string
}

// Tagged reads the turns in [since, until) and tags each. A zero until is
// open.
func Tagged(ctx context.Context, d Deps, since, until time.Time) ([]TaggedTurn, error) {
	turns, err := d.Turns(ctx, since)
	if err != nil {
		return nil, err
	}
	if !until.IsZero() {
		kept := turns[:0]
		for _, t := range turns {
			if t.At.Before(until) {
				kept = append(kept, t)
			}
		}
		turns = kept
	}
	if len(d.Rules) == 0 || len(turns) == 0 {
		return Tag(nil, turns, Place{}), nil
	}
	return Tag(d.Rules, turns, Locate(ctx, d, since)), nil
}

// Locate finds where the sessions since since ran.
func Locate(ctx context.Context, d Deps, since time.Time) Place {
	dirsOf := d.Dirs
	if dirsOf == nil {
		dirsOf = func(s time.Time) map[string]sessiondirs.Dir { return sessiondirs.Find(sessiondirs.Roots{}, s) }
	}
	repos := d.Repos
	if repos == nil {
		repos = gitlog.NewRepos()
	}
	p := Place{Dirs: dirsOf(since), RootOf: map[string]string{}, Names: map[string][]string{}}
	for _, dir := range p.Dirs {
		if _, seen := p.RootOf[dir.CWD]; seen {
			continue
		}
		root := repos.Root(ctx, dir.CWD)
		p.RootOf[dir.CWD] = root
		if _, named := p.Names[root]; root != "" && !named {
			names := []string{filepath.Base(root)}
			if origin := repos.OriginName(ctx, root); origin != "" {
				names = append([]string{origin}, names...)
			}
			p.Names[root] = names
		}
	}
	return p
}

// Tag tags each turn by its session's place: the pure half of Tagged.
func Tag(rules []Rule, turns []analytics.SessionTurn, p Place) []TaggedTurn {
	out := make([]TaggedTurn, 0, len(turns))
	type key struct{ session, source string }
	cache := map[key]TaggedTurn{}
	for _, t := range turns {
		k := key{t.SessionID, t.Source}
		tt, done := cache[k]
		if !done {
			dir, known := p.Dirs[t.SessionID]
			s := dom.Subject{Harness: HarnessOf(t.Source)}
			if known {
				s.Dir, s.Branch = dir.CWD, dir.Branch
				s.Repos = p.Names[p.RootOf[dir.CWD]]
			}
			tt = TaggedTurn{Tags: dom.Evaluate(rules, s), DirKnown: known}
			cache[k] = tt
		}
		tt.SessionTurn = t
		out = append(out, tt)
	}
	return out
}

// Group is what the work carrying one value of a key cost.
type Group struct {
	Value string `json:"value"`
	commits.Cost
	Sessions int `json:"sessions"`
}

// Report is the spend per value of one key over a window.
type Report struct {
	Key   Key       `json:"key"`
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// Rules is how many allocation rules are configured; with none,
	// everything is untagged.
	Rules int `json:"rules"`
	// Groups are the key's values, the most valuable first.
	Groups []Group `json:"groups"`
	// Untagged is the work no rule gave the key a value; UnknownDirectory
	// is the part of it whose session's directory no client recorded.
	Untagged         commits.Cost `json:"untagged"`
	UnknownDirectory commits.Cost `json:"unknown_directory"`
	// TaggedShare is the share of the window's API-equivalent value that
	// carries the key.
	TaggedShare float64 `json:"tagged_share"`
	Method      string  `json:"method"`
}

// DefaultDays is the window when none is asked for.
const DefaultDays = 30

// Compute is the spend per value of key over [since, until).
func Compute(ctx context.Context, d Deps, key Key, since, until time.Time) (Report, error) {
	if until.IsZero() {
		until = time.Now()
	}
	turns, err := Tagged(ctx, d, since, until)
	if err != nil {
		return Report{}, err
	}
	r := ByKey(key, turns)
	r.Since, r.Until, r.Rules = since, until, len(d.Rules)
	return r, nil
}

// ByKey sums tagged turns per value of key.
func ByKey(key Key, turns []TaggedTurn) Report {
	r := Report{Key: key, Groups: []Group{}, Method: Method}
	groups := map[string]*Group{}
	sessions := map[string]map[string]bool{}
	var total, tagged float64
	for _, tt := range turns {
		t := tt.SessionTurn
		// Billed is nothing where a plan covered the turn, and its cost
		// otherwise, priced when none was stored.
		t.CostUSD = t.BilledUSD()
		total += t.APIEquivalentUSD
		v := tt.Tags.Get(key)
		if v == "" {
			r.Untagged.Add(t)
			if !tt.DirKnown {
				r.UnknownDirectory.Add(t)
			}
			continue
		}
		g := groups[v]
		if g == nil {
			g = &Group{Value: v}
			groups[v] = g
			sessions[v] = map[string]bool{}
		}
		g.Add(t)
		sessions[v][t.SessionID] = true
		tagged += t.APIEquivalentUSD
	}
	for v, g := range groups {
		g.Sessions = len(sessions[v])
		r.Groups = append(r.Groups, *g)
	}
	sort.Slice(r.Groups, func(i, j int) bool {
		if r.Groups[i].APIEquivalentUSD != r.Groups[j].APIEquivalentUSD {
			return r.Groups[i].APIEquivalentUSD > r.Groups[j].APIEquivalentUSD
		}
		return r.Groups[i].Value < r.Groups[j].Value
	})
	if total > 0 {
		r.TaggedShare = tagged / total
	}
	return r
}

// Coverage is the share of a window's API-equivalent value that carries
// any allocation tag, and each key.
type Coverage struct {
	Rules int `json:"rules"`
	// TaggedPct carries at least one key; ByKeyPct each key.
	TaggedPct float64            `json:"tagged_pct"`
	ByKeyPct  map[string]float64 `json:"by_key_pct"`
}

// CoverageOf measures how much of the turns' value the rules tag.
func CoverageOf(rules int, turns []TaggedTurn) Coverage {
	c := Coverage{Rules: rules, ByKeyPct: map[string]float64{}}
	var total, any float64
	byKey := map[Key]float64{}
	for _, t := range turns {
		total += t.APIEquivalentUSD
		if !t.Tags.Empty() {
			any += t.APIEquivalentUSD
		}
		for _, k := range Keys {
			if t.Tags.Get(k) != "" {
				byKey[k] += t.APIEquivalentUSD
			}
		}
	}
	if total <= 0 {
		return c
	}
	c.TaggedPct = any / total * 100
	for _, k := range Keys {
		c.ByKeyPct[string(k)] = byKey[k] / total * 100
	}
	return c
}
