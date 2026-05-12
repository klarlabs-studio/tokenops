package rules

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// SelectionSignals describes the request the router is selecting rules for.
// Every field is optional — missing signals simply skip the matchers that
// would have used them. The signals are intentionally lightweight so
// callers (proxy, MCP server, replay harness) can populate whatever they
// have without coupling to a full request type.
type SelectionSignals struct {
	// WorkflowID and AgentID identify the active workflow/agent.
	WorkflowID string
	AgentID    string
	// RepoID, when set, scopes selection to that repository.
	RepoID string
	// FilePaths lists the files the request touches (e.g. code files
	// being edited). Used by the file-glob matcher.
	FilePaths []string
	// Tools lists tool names invoked by the request (e.g. "bash",
	// "search"). Used by the tool-scope matcher.
	Tools []string
	// Keywords are free-text tokens extracted from the request prompt
	// or system message. Matched (case-insensitive substring) against
	// rule anchors and bodies.
	Keywords []string
}

// RouterConfig tunes the router.
type RouterConfig struct {
	// TokenBudget caps the total token cost of the selected rule subset.
	// Zero means unbounded. The router admits sections in score-desc
	// order and stops once the budget is reached.
	TokenBudget int64
	// MinScore is the score threshold a candidate section must clear to
	// be considered. Sections that match no signal land at 0; anything
	// at or above this threshold is admitted (subject to budget).
	// Default 1.0 (require at least one signal hit).
	MinScore float64
	// LatencyBudget caps the wall-clock time the router is allowed to
	// spend. The router walks documents in order; when the budget is
	// exhausted it returns the partial result with Truncated=true.
	// Zero means unbounded.
	LatencyBudget time.Duration
	// IncludeGlobalScope, when true, always admits sections from
	// global-scoped documents even when no signal hits. Useful for
	// non-negotiable house rules.
	IncludeGlobalScope bool
	// Now returns the wall clock used for latency budgeting. Defaults
	// to time.Now.
	Now func() time.Time
}

func (c RouterConfig) withDefaults() RouterConfig {
	if c.MinScore == 0 {
		c.MinScore = 1.0
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Selection is one section the router chose to inject, along with the
// rationale that placed it.
type Selection struct {
	SourceID   string
	SectionID  string
	Anchor     string
	TokenCount int64
	Score      float64
	Reasons    []string
}

// SelectionResult bundles a routing decision.
type SelectionResult struct {
	Selections   []Selection
	SkippedCount int
	TotalTokens  int64
	Truncated    bool
	BudgetHit    bool
	ElapsedNS    int64
	Considered   int
}

// Router picks a relevant subset of rules for each request.
type Router struct {
	cfg RouterConfig
}

// NewRouter constructs a Router with the given configuration.
func NewRouter(cfg RouterConfig) *Router { return &Router{cfg: cfg.withDefaults()} }

// Select runs the matchers across docs and returns the chosen subset.
// docs is the corpus snapshot (typically the output of Ingestor.Snapshot
// or the compressor's surviving sections).
func (r *Router) Select(docs []*RuleDocument, sig SelectionSignals) *SelectionResult {
	start := r.cfg.Now()
	res := &SelectionResult{}

	type candidate struct {
		sel Selection
	}
	var cands []candidate

	deadline := time.Time{}
	if r.cfg.LatencyBudget > 0 {
		deadline = start.Add(r.cfg.LatencyBudget)
	}

	for _, d := range docs {
		if !deadline.IsZero() && r.cfg.Now().After(deadline) {
			res.Truncated = true
			break
		}
		if sig.RepoID != "" && d.RepoID != "" && d.RepoID != sig.RepoID {
			continue
		}
		for _, b := range d.Blocks {
			res.Considered++
			score, reasons := r.score(d, b, sig)
			if score < r.cfg.MinScore {
				res.SkippedCount++
				continue
			}
			cands = append(cands, candidate{sel: Selection{
				SourceID:   d.SourceID,
				SectionID:  b.ID(d.SourceID),
				Anchor:     b.Anchor,
				TokenCount: int64(len(b.Body)) / 4, // char-heuristic; downstream uses tokenizer for authoritative counts
				Score:      score,
				Reasons:    reasons,
			}})
		}
	}

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].sel.Score != cands[j].sel.Score {
			return cands[i].sel.Score > cands[j].sel.Score
		}
		return cands[i].sel.SectionID < cands[j].sel.SectionID
	})

	for _, c := range cands {
		if r.cfg.TokenBudget > 0 && res.TotalTokens+c.sel.TokenCount > r.cfg.TokenBudget {
			res.BudgetHit = true
			res.SkippedCount++
			continue
		}
		res.Selections = append(res.Selections, c.sel)
		res.TotalTokens += c.sel.TokenCount
	}

	res.ElapsedNS = r.cfg.Now().Sub(start).Nanoseconds()
	return res
}

// score returns the relevance score of block b in document d given the
// selection signals. Each contributing matcher adds 1.0 to the score and
// pushes a reason string onto reasons. Sections from globally-scoped docs
// receive a 1.0 floor when IncludeGlobalScope is set.
func (r *Router) score(d *RuleDocument, b RuleBlock, sig SelectionSignals) (float64, []string) {
	score := 0.0
	var reasons []string

	if r.cfg.IncludeGlobalScope && d.Scope == eventschema.RuleScopeGlobal {
		score += 1.0
		reasons = append(reasons, "scope:global")
	}

	if sig.RepoID != "" && d.RepoID == sig.RepoID {
		score += 0.5
		reasons = append(reasons, "repo_match")
	}

	if matchAnyGlob(d.Path, sig.FilePaths) {
		score += 1.0
		reasons = append(reasons, "path:"+d.Path)
	}

	bodyLower := strings.ToLower(b.Body)
	anchorLower := strings.ToLower(b.Anchor)

	for _, tool := range sig.Tools {
		t := strings.ToLower(tool)
		if t == "" {
			continue
		}
		if strings.Contains(bodyLower, t) || strings.Contains(anchorLower, t) {
			score += 1.0
			reasons = append(reasons, "tool:"+tool)
		}
	}

	for _, kw := range sig.Keywords {
		k := strings.ToLower(strings.TrimSpace(kw))
		if k == "" {
			continue
		}
		hit := false
		if strings.Contains(anchorLower, k) {
			score += 1.0
			hit = true
		}
		if strings.Contains(bodyLower, k) {
			score += 0.5
			hit = true
		}
		if hit {
			reasons = append(reasons, "kw:"+kw)
		}
	}

	if sig.WorkflowID != "" && strings.Contains(anchorLower, strings.ToLower(sig.WorkflowID)) {
		score += 0.5
		reasons = append(reasons, "workflow:"+sig.WorkflowID)
	}

	return score, reasons
}

// matchAnyGlob reports whether any path in candidates matches the glob
// pattern (forward-slash, supports * and **).
func matchAnyGlob(pattern string, candidates []string) bool {
	if pattern == "" || len(candidates) == 0 {
		return false
	}
	// Treat a plain rule-path as a literal match when one of the
	// candidate file paths shares the same trailing path or extension
	// hint. For Cursor-style rules whose Path is ".cursor/rules/go.mdc"
	// we look for the extension token inside candidate file paths.
	ext := strings.TrimPrefix(filepath.Ext(pattern), ".")
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if c == pattern {
			return true
		}
		if ext != "" && strings.HasSuffix(strings.ToLower(c), "."+strings.ToLower(ext)) {
			return true
		}
		if strings.Contains(c, pattern) {
			return true
		}
	}
	return false
}

// AsAnalysisEvents renders the routing decision as RuleAnalysisEvent
// payloads so the selection rationale flows through the same telemetry
// pipeline as ROI snapshots. One event per selection.
func (r *SelectionResult) AsAnalysisEvents(window time.Time) []*eventschema.RuleAnalysisEvent {
	out := make([]*eventschema.RuleAnalysisEvent, 0, len(r.Selections))
	for _, s := range r.Selections {
		out = append(out, &eventschema.RuleAnalysisEvent{
			SourceID:      s.SourceID,
			SectionID:     s.SectionID,
			Exposures:     1,
			ContextTokens: s.TokenCount,
			WindowStart:   window,
			WindowEnd:     window,
			ROIScore:      s.Score,
		})
	}
	return out
}
