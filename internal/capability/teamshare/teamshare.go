// Package teamshare computes what this machine uploads to a team plane
// (ADR 0012): per UTC day, repository label and kind of work, the sums of
// its agent work — instructions, turns, outcomes, time, tokens, money.
//
// The operator's instructions are read here, at scan time, only to
// classify each one's kind of work; the text never leaves this package.
// Session directories are read only to find the repository, which is
// reduced to an owner/name label. What comes out is a teamwire.Upload, a
// type with no field that can hold anything else. The daemon's uploader,
// `tokenops team preview` and `tokenops team sync` all build it here.
package teamshare

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Repository naming modes; they match config.Team* values.
const (
	RepoRemote    = "remote"
	RepoDirectory = "directory"
	RepoHidden    = "hidden"
)

// Options shape an upload.
type Options struct {
	// Days is how many UTC days, today included, the upload covers.
	Days int
	// RepoNames is remote, directory or hidden.
	RepoNames string
	// ClientVersion is this TokenOps' version.
	ClientVersion string
}

// Repos resolves directories to repositories; *gitlog.Repos satisfies it.
type Repos interface {
	Root(ctx context.Context, dir string) string
	OriginName(ctx context.Context, root string) string
}

// Deps are the readers an upload is computed from.
type Deps struct {
	// Units are the instructions since a time, with their text.
	Units func(since, now time.Time) ([]sessions.Unit, error)
	// Turns are the priced turns since a time.
	Turns func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)
	// Dirs maps sessions to where they ran.
	Dirs  func(since time.Time) map[string]sessiondirs.Dir
	Repos Repos
}

// DepsFor reads this machine's transcripts, its event store through agg,
// and git.
func DepsFor(agg *analytics.Aggregator) Deps {
	return Deps{
		Units: func(since, now time.Time) ([]sessions.Unit, error) {
			days := int(now.Sub(since).Hours()/24) + 1
			return sessions.Units(sessions.Window{Days: days}, now)
		},
		Turns: func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
			if agg == nil {
				return nil, nil
			}
			return agg.SessionTurns(ctx, analytics.Filter{Since: since})
		},
		Dirs:  func(since time.Time) map[string]sessiondirs.Dir { return sessiondirs.Find(sessiondirs.Roots{}, since) },
		Repos: gitlog.NewRepos(),
	}
}

// CoveredDays are the n UTC days ending today, oldest first.
func CoveredDays(now time.Time, n int) []string {
	if n <= 0 {
		n = 14
	}
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	out := make([]string, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, today.AddDate(0, 0, -i).Format(teamwire.DayLayout))
	}
	return out
}

// Build computes the upload for the days opts covers. A reader that fails
// leaves its part out rather than failing the upload: a machine without
// transcripts still uploads its spend.
func Build(ctx context.Context, d Deps, opts Options, now time.Time) (teamwire.Upload, []string) {
	days := CoveredDays(now, opts.Days)
	since, _ := time.Parse(teamwire.DayLayout, days[0])
	var warnings []string
	var units []sessions.Unit
	if d.Units != nil {
		var err error
		units, err = d.Units(since, now)
		if err != nil {
			warnings = append(warnings, "transcripts: "+err.Error())
		}
	}
	var turns []analytics.SessionTurn
	if d.Turns != nil {
		var err error
		turns, err = d.Turns(ctx, since)
		if err != nil {
			warnings = append(warnings, "event store: "+err.Error())
		}
	}
	repoOf := map[string]string{}
	if d.Dirs != nil && d.Repos != nil {
		names := map[string]string{}
		for id, dir := range d.Dirs(since) {
			label, ok := names[dir.CWD]
			if !ok {
				label = repoLabel(ctx, d.Repos, dir.CWD, opts.RepoNames)
				names[dir.CWD] = label
			}
			repoOf[id] = label
		}
	}
	return Aggregate(units, turns, repoOf, days, opts.ClientVersion, now), warnings
}

var unsafeLabel = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeSegment reduces a name to the label alphabet.
func sanitizeSegment(s string) string {
	s = strings.Trim(unsafeLabel.ReplaceAllString(s, "-"), "-._")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// repoLabel names the repository dir lies in, as mode allows.
func repoLabel(ctx context.Context, repos Repos, dir, mode string) string {
	if dir == "" {
		return teamwire.RepoNone
	}
	root := repos.Root(ctx, dir)
	if root == "" {
		return teamwire.RepoNone
	}
	if mode == RepoHidden {
		return teamwire.RepoHidden
	}
	var label string
	if mode != RepoDirectory {
		if owner, name, ok := strings.Cut(repos.OriginName(ctx, root), "/"); ok {
			if o, n := sanitizeSegment(owner), sanitizeSegment(name); o != "" && n != "" {
				label = o + "/" + n
			}
		}
	}
	if label == "" {
		label = sanitizeSegment(filepath.Base(root))
	}
	if !teamwire.ValidRepo(label) {
		return teamwire.RepoHidden
	}
	return label
}

type key struct{ day, repo, kind string }

type acc struct {
	b        teamwire.Bucket
	sessions map[string]bool
}

// Aggregate is the pure half of Build: it files each instruction and each
// turn under its day, repository and kind of work, and sums.
func Aggregate(units []sessions.Unit, turns []analytics.SessionTurn, repoOf map[string]string, days []string, version string, now time.Time) teamwire.Upload {
	covered := map[string]bool{}
	for _, d := range days {
		covered[d] = true
	}
	buckets := map[key]*acc{}
	get := func(day, session string, kind taskclass.Kind) *acc {
		repo := repoOf[session]
		if repo == "" {
			repo = teamwire.RepoNone
		}
		k := key{day, repo, string(kind)}
		a := buckets[k]
		if a == nil {
			a = &acc{b: teamwire.Bucket{Day: day, Repo: repo, Kind: string(kind)}, sessions: map[string]bool{}}
			buckets[k] = a
		}
		a.sessions[session] = true
		return a
	}

	// Classify each session's instructions in order, a continuation
	// keeping the kind of the work in flight.
	bySession := map[string][]sessions.Unit{}
	for _, u := range units {
		bySession[u.SessionID] = append(bySession[u.SessionID], u)
	}
	type span struct {
		start time.Time
		kind  taskclass.Kind
	}
	spans := map[string][]span{}
	for id, us := range bySession {
		sort.SliceStable(us, func(i, j int) bool { return us[i].Start.Before(us[j].Start) })
		prev := taskclass.KindUnknown
		for _, u := range us {
			kind := taskclass.KindForTurn(u.Prompt, prev)
			prev = kind
			spans[id] = append(spans[id], span{u.Start, kind})
			day := u.Start.UTC().Format(teamwire.DayLayout)
			if !covered[day] {
				continue
			}
			a := get(day, id, kind)
			a.b.Instructions++
			a.b.Turns += int64(u.Turns)
			a.b.ToolCalls += int64(u.ToolCalls)
			a.b.ActiveSeconds += u.Duration().Seconds()
			if u.FirstTry() {
				a.b.FirstTry++
			}
			if u.Reworked {
				a.b.Reworked++
			}
			if u.Interrupted {
				a.b.Interrupted++
			}
			if u.Escalated {
				a.b.Escalated++
			}
			if u.Rejected {
				a.b.Rejected++
			}
		}
	}
	for _, t := range turns {
		day := t.At.UTC().Format(teamwire.DayLayout)
		if !covered[day] {
			continue
		}
		kind := taskclass.KindUnknown
		ss := spans[t.SessionID]
		// The instruction a turn answers is the last one typed before it.
		if i := sort.Search(len(ss), func(i int) bool { return ss[i].start.After(t.At) }); i > 0 {
			kind = ss[i-1].kind
		}
		a := get(day, t.SessionID, kind)
		a.b.Tokens += t.Tokens
		if t.Priced {
			a.b.CostUSD += t.CostUSD
			a.b.APIEquivalentUSD += t.APIEquivalentUSD
		} else {
			a.b.UnpricedTurns++
		}
	}

	out := teamwire.Upload{Schema: teamwire.SchemaVersion, BatchID: uuid.NewString(), ComputedAt: now.UTC(),
		Days: days, Buckets: make([]teamwire.Bucket, 0, len(buckets))}
	if teamwire.ValidLabel(version) {
		out.ClientVersion = version
	}
	for _, a := range buckets {
		a.b.Sessions = int64(len(a.sessions))
		out.Buckets = append(out.Buckets, a.b)
	}
	sort.Slice(out.Buckets, func(i, j int) bool {
		x, y := out.Buckets[i], out.Buckets[j]
		if x.Day != y.Day {
			return x.Day < y.Day
		}
		if x.Repo != y.Repo {
			return x.Repo < y.Repo
		}
		return x.Kind < y.Kind
	})
	return out
}
