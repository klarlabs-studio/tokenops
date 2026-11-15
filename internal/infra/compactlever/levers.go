package compactlever

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"time"

	_ "modernc.org/sqlite" // opencode's store

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pisessions"
	"go.klarlabs.de/tokenops/internal/sqliteuri"
)

// OpencodeShare is where an opencode model compacts, as a share of its
// context window: the same 60% the Claude Code (600k of 1M) and Codex
// (150k of 250k) lines stand at.
const OpencodeShare = 0.6

// opencodeLookback bounds which opencode models count as in use.
const opencodeLookback = 30 * 24 * time.Hour

// Levers is the coach's ContextLevers port over the agents' settings
// files, planned from the operator's config.
type Levers struct {
	Paths Paths
	Plan  Plan
	Now   func() time.Time
	// opencodeModels lists the opencode models run here, for Apply. It is
	// resolved only when the levers are applied: listing them scans
	// opencode's message store (above a gigabyte on a busy machine), and
	// Check, which every coach report calls, never reads them. Resolving it
	// in New made each GET /api/coach and /api/findings wait up to two
	// seconds for a list it threw away. nil keeps Plan.OpencodeModels.
	opencodeModels func() []string
	// piModels lists the Pi models run here, for Apply and Preview, for the
	// same reason: it reads Pi's session files.
	piModels func() []string
}

var _ coachcap.ContextLevers = Levers{}

// New plans the levers from cfg: the compaction lines the compact tip and
// the compact_earlier finding use, the developer's own derived context line
// when one is measured, and the opencode models run here, which Apply lists
// when it runs.
func New(cfg config.Config) (Levers, error) {
	p, err := DefaultPaths()
	if err != nil {
		return Levers{}, err
	}
	return Levers{
		Paths: p,
		Plan:  planFor(cfg, snapshotDir(), time.Now()),
		Now:   time.Now,
		opencodeModels: func() []string {
			return opencodeModelsUsed(time.Now().Add(-opencodeLookback))
		},
		piModels: func() []string {
			return piModelsUsed(time.Now().Add(-opencodeLookback))
		},
	}, nil
}

// Apply implements coachcap.ContextLevers.
func (l Levers) Apply() ([]coachcap.LeverResult, error) {
	rs, err := Apply(l.Paths, l.plan(), l.now())
	return results(rs), err
}

// plan is Plan with the opencode models resolved.
func (l Levers) plan() Plan {
	p := l.Plan
	if l.opencodeModels != nil {
		p.OpencodeModels = l.opencodeModels()
	}
	if l.piModels != nil && p.PiCompactAt > 0 {
		p.PiModels = l.piModels()
	}
	return p
}

// Preview implements coachcap.ContextLevers: what Apply would write, with
// nothing written.
func (l Levers) Preview() ([]coachcap.LeverResult, error) {
	rs, err := Preview(l.Paths, l.plan(), l.now())
	return results(rs), err
}

// Revert implements coachcap.ContextLevers.
func (l Levers) Revert() ([]coachcap.LeverResult, error) {
	rs, err := Revert(l.Paths)
	return results(rs), err
}

// Check implements coachcap.ContextLevers.
func (l Levers) Check() []coachcap.LeverResult { return results(Check(l.Paths)) }

func (l Levers) now() time.Time {
	if l.Now == nil {
		return time.Now()
	}
	return l.Now()
}

func results(rs []Result) []coachcap.LeverResult {
	out := make([]coachcap.LeverResult, 0, len(rs))
	for _, r := range rs {
		out = append(out, coachcap.LeverResult{Client: r.Client, Key: r.Key, Value: r.Value, Status: string(r.Status), Note: r.Note})
	}
	return out
}

// opencodeModelsUsed lists the provider/model pairs opencode has run on
// since the cutoff, from its own store. None when opencode is not here.
func opencodeModelsUsed(since time.Time) []string {
	path, err := opencode.DefaultRoot()
	if err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", sqliteuri.File(path, "mode=ro"))
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT json_extract(data,'$.providerID'), json_extract(data,'$.modelID')
		FROM message WHERE time_created > ? AND json_extract(data,'$.role') = 'assistant'`, since.UnixMilli())
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var prov, model sql.NullString
		if rows.Scan(&prov, &model) == nil && prov.String != "" && model.String != "" {
			out = append(out, prov.String+"/"+model.String)
		}
	}
	slices.Sort(out)
	return out
}

// piModelsUsed lists the provider/modelId pairs Pi has run on since the
// cutoff, from its own session files modified since then. None when Pi is
// not here.
func piModelsUsed(since time.Time) []string {
	root, err := pisessions.PiRoot()
	if err != nil {
		return nil
	}
	files, err := pisessions.FindSessionFiles(root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, f := range files {
		if fi, err := os.Stat(f); err != nil || fi.ModTime().Before(since) {
			continue
		}
		_ = pisessions.ReadFile(f, func(t pisessions.Turn) error {
			if t.Backend != "" && t.ModelID != "" && !t.Timestamp.Before(since) {
				seen[t.Backend+"/"+t.ModelID] = true
			}
			return nil
		})
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
