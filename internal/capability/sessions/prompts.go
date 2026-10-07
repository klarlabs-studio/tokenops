package sessions

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts"
	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
)

// PromptWindow selects which typed instructions to score.
type PromptWindow struct {
	Root      string
	SessionID string
	Limit     int
	// Since defaults to seven days before now; Until is open when zero.
	Since, Until time.Time
	// Source restricts the scan to one client (claude-code, codex,
	// opencode); empty reads them all.
	Source string
	// IncludeScratch keeps sessions run in throwaway directories.
	IncludeScratch bool
}

// extractOptions is w as the prompts extractor reads it.
func (w PromptWindow) extractOptions(now time.Time) prompts.ExtractOptions {
	opts := prompts.ExtractOptions{
		Root: w.Root, Source: prompts.Source(w.Source), SessionID: w.SessionID, Limit: w.Limit,
		Since: w.Since, Until: w.Until, IncludeScratch: w.IncludeScratch, Opencode: opencodestore.Store{},
	}
	if opts.Since.IsZero() {
		opts.Since = now.Add(-7 * 24 * time.Hour)
	}
	return opts
}

// PromptFindings scores the operator's typed instructions against the
// prompting heuristics: length, vague or acknowledging turns, repeats, and
// what to change. Without text, every quoted instruction is left out and
// only counts and recommendations remain, as the daemon API serves them
// (ADR 0010 §5). The text is read at scan time and never persisted.
func PromptFindings(w PromptWindow, withText bool, now time.Time) (prompts.Findings, error) {
	extracted, err := prompts.Extract(w.extractOptions(now))
	if err != nil {
		return prompts.Findings{}, err
	}
	f := prompts.Analyze(extracted)
	if !withText {
		f = withoutText(f)
	}
	return f, nil
}

// withoutText drops every quoted instruction from f.
func withoutText(f prompts.Findings) prompts.Findings {
	f.VagueShortSamples = nil
	f.RegenerateSamples = nil
	repeated := make([]prompts.RepeatedItem, 0, len(f.RepeatedPrompts))
	for _, r := range f.RepeatedPrompts {
		repeated = append(repeated, prompts.RepeatedItem{Count: r.Count})
	}
	f.RepeatedPrompts = repeated
	recs := make([]prompts.Recommendation, 0, len(f.Recommendations))
	for _, r := range f.Recommendations {
		r.Evidence = nil
		recs = append(recs, r)
	}
	f.Recommendations = recs
	return f
}

// Findings is the prompt scoring answer, named here so adapters can
// describe it without reaching into the coaching domain.
type Findings = prompts.Findings

// TurnStats is what an average assistant turn in the window cost, the
// basis for projecting a recommendation's savings.
type TurnStats = prompts.TurnStats

// PromptRecommendation is one change to how the operator prompts.
type PromptRecommendation = prompts.Recommendation

// SavingsEstimate is a recommendation's projected monthly savings.
type SavingsEstimate = prompts.SavingsEstimate

// PromptTurnStats averages the assistant turns in w: tokens, cost and
// time.
func PromptTurnStats(w PromptWindow, now time.Time) (TurnStats, error) {
	return prompts.ComputeTurnStats(w.extractOptions(now))
}

// ProjectSavings is what following r would save a month, at the turn
// cost in stats.
func ProjectSavings(r PromptRecommendation, stats TurnStats) SavingsEstimate {
	return prompts.ProjectSavings(r, stats)
}
