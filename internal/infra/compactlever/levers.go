package compactlever

import (
	"context"
	"database/sql"
	"slices"
	"time"

	_ "modernc.org/sqlite" // opencode's store

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
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
}

var _ coachcap.ContextLevers = Levers{}

// New plans the levers from cfg: the compaction lines the compact tip and
// the compact_earlier finding use, and the opencode models run here.
func New(cfg config.Config) (Levers, error) {
	p, err := DefaultPaths()
	if err != nil {
		return Levers{}, err
	}
	return Levers{
		Paths: p,
		Plan: Plan{
			ClaudeCompactAt: coachcap.CompactAt(cfg, "claude-code:"),
			CodexCompactAt:  coachcap.CompactAt(cfg, "codex:"),
			OpencodeModels:  opencodeModelsUsed(time.Now().Add(-opencodeLookback)),
			OpencodeShare:   OpencodeShare,
		},
		Now: time.Now,
	}, nil
}

// Apply implements coachcap.ContextLevers.
func (l Levers) Apply() ([]coachcap.LeverResult, error) {
	rs, err := Apply(l.Paths, l.Plan, l.now())
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
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
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
