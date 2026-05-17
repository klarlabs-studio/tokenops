package prompts

import (
	"sort"
	"strings"
)

// Findings is the analyzer's report: aggregate stats + ranked
// recommendations. Designed for JSON rendering (CLI --json, MCP tool
// result) so the field names are the rendering contract — don't
// rename without bumping the MCP tool minor version.
type Findings struct {
	TotalPrompts       int              `json:"total_prompts"`
	AvgChars           float64          `json:"avg_chars"`
	AvgWords           float64          `json:"avg_words"`
	MinChars           int              `json:"min_chars"`
	MaxChars           int              `json:"max_chars"`
	LengthDistribution map[string]int   `json:"length_distribution"`
	VagueShort         int              `json:"vague_short_count"`
	VagueShortSamples  []string         `json:"vague_short_samples,omitempty"`
	Acknowledgements   int              `json:"acknowledgement_count"`
	ShortQuestions     int              `json:"short_question_count"`
	NoContextSingles   int              `json:"no_context_singles_count"`
	RepeatedPrompts    []RepeatedItem   `json:"repeated_prompts,omitempty"`
	Recommendations    []Recommendation `json:"recommendations"`
}

// Recommendation is one actionable suggestion grounded in the
// operator's own data. Sorted by ImpactScore descending so the CLI
// can lead with the biggest-win finding without re-sorting.
//
// JSON shape is the public contract for the MCP tool result.
type Recommendation struct {
	ID                         string   `json:"id"`
	Title                      string   `json:"title"`
	Why                        string   `json:"why"`
	Evidence                   []string `json:"evidence,omitempty"`
	Frequency                  int      `json:"frequency"`
	ImpactScore                float64  `json:"impact_score"`
	EstimatedMonthlyTurnsSaved int      `json:"estimated_monthly_turns_saved"`
	Before                     string   `json:"before,omitempty"`
	After                      string   `json:"after,omitempty"`
}

// RepeatedItem captures a verbatim-repeated prompt + how often it
// appeared. Sorted descending by Count for stable JSON output.
type RepeatedItem struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// ackSet contains the literal prompt strings we treat as pure
// confirmation steering. Lowercased on lookup; comparisons are
// trimmed. Intentionally narrow — phrases longer than "yes please"
// are usually answering a real question.
var ackSet = map[string]struct{}{
	"yes": {}, "no": {}, "ok": {}, "okay": {},
	"continue": {}, "go": {}, "proceed": {}, "sure": {},
	"yes please": {}, "do it": {}, "go ahead": {}, "keep going": {},
}

// vagueDirectiveSet matches short, scope-free directive prompts.
// These force the agent to invent scope — flagged separately from
// pure acks so the rewrite template can be specific ("fix X" not
// "yes/no batch").
var vagueDirectiveSet = map[string]struct{}{
	"fix all": {}, "fix it all": {}, "fix it": {}, "do it": {},
	"go": {}, "merge it": {}, "ship it": {}, "commit it": {},
	"commit and push": {}, "make it work": {},
}

// Analyze returns the Findings for a slice of UserPrompt. Pure
// function: no IO, no clock. Test by feeding fixed slices.
func Analyze(prompts []UserPrompt) Findings {
	f := Findings{
		LengthDistribution: map[string]int{
			"<5w":     0,
			"5-15w":   0,
			"15-50w":  0,
			"50-200w": 0,
			">200w":   0,
		},
	}
	if len(prompts) == 0 {
		f.Recommendations = []Recommendation{{
			ID:    "no_data",
			Title: "no user prompts in window — nothing to analyze",
		}}
		return f
	}
	f.TotalPrompts = len(prompts)

	var charSum, wordSum int
	f.MinChars = len(prompts[0].Text)
	repeatCounts := map[string]int{}

	var (
		vagueSamples       []string
		ackSamples         []string
		vagueDirectives    []string
		acks               int
		shortQs            int
		noCtx              int
		vagueDirectiveHits int
		shortNoFileRef     int
	)

	for _, p := range prompts {
		txt := strings.TrimSpace(p.Text)
		nc := len(txt)
		nw := len(strings.Fields(txt))
		charSum += nc
		wordSum += nw
		if nc < f.MinChars {
			f.MinChars = nc
		}
		if nc > f.MaxChars {
			f.MaxChars = nc
		}
		switch {
		case nw < 5:
			f.LengthDistribution["<5w"]++
		case nw < 15:
			f.LengthDistribution["5-15w"]++
		case nw < 50:
			f.LengthDistribution["15-50w"]++
		case nw < 200:
			f.LengthDistribution["50-200w"]++
		default:
			f.LengthDistribution[">200w"]++
		}

		lc := strings.ToLower(txt)
		if _, ok := ackSet[lc]; ok {
			acks++
			if len(ackSamples) < 3 {
				ackSamples = append(ackSamples, txt)
			}
		}
		if _, ok := vagueDirectiveSet[lc]; ok {
			vagueDirectiveHits++
			if len(vagueDirectives) < 3 {
				vagueDirectives = append(vagueDirectives, txt)
			}
		}
		if nc < 15 && nw <= 3 {
			f.VagueShort++
			if len(vagueSamples) < 3 {
				vagueSamples = append(vagueSamples, txt)
			}
		}
		if nc < 60 && strings.Contains(txt, "?") {
			shortQs++
		}
		if nc > 5 && nc < 30 && !strings.ContainsAny(txt, ".,:;()") {
			noCtx++
		}
		if nw >= 3 && nw <= 20 && !looksLikeFileRef(txt) {
			shortNoFileRef++
		}
		if nc > 5 {
			repeatCounts[lc]++
		}
	}
	f.AvgChars = float64(charSum) / float64(len(prompts))
	f.AvgWords = float64(wordSum) / float64(len(prompts))
	f.VagueShortSamples = vagueSamples
	f.Acknowledgements = acks
	f.ShortQuestions = shortQs
	f.NoContextSingles = noCtx

	for t, c := range repeatCounts {
		if c >= 3 {
			f.RepeatedPrompts = append(f.RepeatedPrompts, RepeatedItem{Text: t, Count: c})
		}
	}
	sort.Slice(f.RepeatedPrompts, func(i, j int) bool {
		if f.RepeatedPrompts[i].Count != f.RepeatedPrompts[j].Count {
			return f.RepeatedPrompts[i].Count > f.RepeatedPrompts[j].Count
		}
		return f.RepeatedPrompts[i].Text < f.RepeatedPrompts[j].Text
	})
	if len(f.RepeatedPrompts) > 10 {
		f.RepeatedPrompts = f.RepeatedPrompts[:10]
	}

	f.Recommendations = buildRecommendations(f, ackSamples, vagueDirectives, vagueDirectiveHits, shortNoFileRef)
	return f
}

// looksLikeFileRef is a fast heuristic: does the prompt mention a
// path-like token (slash + extension, or `pkg/X.go:42`)? Used by
// the cite-files recommendation so prompts that already cite a file
// don't count against the operator.
func looksLikeFileRef(s string) bool {
	for _, ext := range []string{".go", ".ts", ".tsx", ".js", ".py", ".rs", ".md", ".yaml", ".yml", ".json", ".sql", ".sh", ".html", ".css"} {
		if strings.Contains(s, ext) {
			return true
		}
	}
	return strings.Contains(s, "pkg/") || strings.Contains(s, "internal/") || strings.Contains(s, "src/") || strings.Contains(s, "cmd/")
}

// buildRecommendations produces the ranked list. Each rule that fires
// emits one Recommendation with evidence pulled from the operator's
// data + a per-rule monthly-savings estimate (derived from the
// frequency assuming a 30-day window, conservative).
//
// Sort key: ImpactScore = frequency × signal-weight. Order is the
// public output order; renderers can lead with the biggest win.
func buildRecommendations(f Findings, ackSamples, vagueDirectives []string, vagueDirectiveHits, shortNoFileRef int) []Recommendation {
	if f.TotalPrompts == 0 {
		return []Recommendation{{ID: "no_data", Title: "no data"}}
	}
	var recs []Recommendation

	// Rule 1: vague directives ("fix all", "do it") force the agent
	// to invent scope. Each one costs ~3 clarification rounds.
	if vagueDirectiveHits > 0 {
		recs = append(recs, Recommendation{
			ID:                         "scope_vague_directives",
			Title:                      "Scope your directives instead of saying \"fix all\" or \"do it\"",
			Why:                        "Single-verb directives force the agent to invent scope. Each one usually costs ~3 clarification rounds before the agent acts.",
			Evidence:                   vagueDirectives,
			Frequency:                  vagueDirectiveHits,
			ImpactScore:                float64(vagueDirectiveHits) * 3.0,
			EstimatedMonthlyTurnsSaved: vagueDirectiveHits * 3,
			Before:                     "fix all",
			After:                      "fix items #1, #2 from the gap list — skip #3 + #4",
		})
	}

	// Rule 2: pure acks dominate the turn stream. Many are real
	// choices, but if >15% of turns are yes/no, the agent is asking
	// too many confirmations.
	ackRatio := float64(f.Acknowledgements) / float64(f.TotalPrompts)
	if ackRatio > 0.15 {
		extraAcks := f.Acknowledgements - int(0.10*float64(f.TotalPrompts)) // savings = acks beyond a healthy 10% baseline
		if extraAcks < 0 {
			extraAcks = 0
		}
		recs = append(recs, Recommendation{
			ID:                         "reduce_confirmation_loops",
			Title:                      "Pre-empt confirmation gates",
			Why:                        "Pure yes/no/ok/continue/proceed prompts steer the agent through gates it could decide on its own. State the standing policy in the initial brief.",
			Evidence:                   ackSamples,
			Frequency:                  f.Acknowledgements,
			ImpactScore:                float64(extraAcks) * 1.0,
			EstimatedMonthlyTurnsSaved: extraAcks,
			Before:                     "yes",
			After:                      "no confirmation needed unless destructive (force-push, rm, drop table)",
		})
	}

	// Rule 3: same prompt issued 3+ times means the agent didn't act
	// on the first ask. Re-issuing the same verb is wasted; the fix
	// is escalation, not repetition.
	if len(f.RepeatedPrompts) > 0 {
		// Sum extra occurrences (after the first) — that's the wasted
		// turn count this rule would eliminate.
		var wasted int
		var ev []string
		for i, r := range f.RepeatedPrompts {
			wasted += r.Count - 1
			if i < 3 {
				ev = append(ev, r.Text)
			}
		}
		recs = append(recs, Recommendation{
			ID:                         "stop_repeating",
			Title:                      "Don't re-issue identical prompts",
			Why:                        "When the agent doesn't act on the first ask, repeating the same verb wastes a turn. Escalate the instruction instead.",
			Evidence:                   ev,
			Frequency:                  wasted,
			ImpactScore:                float64(wasted) * 1.5,
			EstimatedMonthlyTurnsSaved: wasted,
			Before:                     "merge it",
			After:                      "merge PR #X --squash --admin without asking; then push to main",
		})
	}

	// Rule 4: short turns that don't cite a file path / function /
	// line number. Cost: 3-4 wasted iterations to narrow the target.
	// Requires at least 20 prompts so the rule doesn't false-fire on
	// small samples (a 3-prompt unit test would always trip 30%).
	if f.TotalPrompts >= 20 && shortNoFileRef > 0 && float64(shortNoFileRef)/float64(f.TotalPrompts) > 0.30 {
		recs = append(recs, Recommendation{
			ID:                         "cite_file_paths",
			Title:                      "Cite file paths + line numbers",
			Why:                        "Short prompts that don't reference a file force the agent to guess the target. A path + line cost a few extra tokens but cut 3-4 iterations of disambiguation.",
			Evidence:                   nil,
			Frequency:                  shortNoFileRef,
			ImpactScore:                float64(shortNoFileRef) * 0.3,
			EstimatedMonthlyTurnsSaved: shortNoFileRef / 4,
			Before:                     "fix it",
			After:                      "refactor pkg/x/handler.go:retryLoop into a helper",
		})
	}

	// Rule 5: low-density coaching — avg prompt is very short.
	// Fallback when nothing else fires (or as a tail item) so the
	// operator with healthy patterns still gets one nudge.
	if f.AvgWords < 10 && f.TotalPrompts > 20 && len(recs) == 0 {
		recs = append(recs, Recommendation{
			ID:                         "front_load_context",
			Title:                      "Front-load context in your initial brief",
			Why:                        "Average prompt length is under 10 words. Stronger prompts include the file, the goal, the constraint, and the success criteria up front.",
			Frequency:                  f.LengthDistribution["<5w"],
			ImpactScore:                float64(f.LengthDistribution["<5w"]) * 0.2,
			EstimatedMonthlyTurnsSaved: f.LengthDistribution["<5w"] / 5,
			Before:                     "do it",
			After:                      "refactor handler.go to return errors instead of panicking. Keep existing call sites. Add a table test.",
		})
	}

	sort.SliceStable(recs, func(i, j int) bool {
		return recs[i].ImpactScore > recs[j].ImpactScore
	})

	if len(recs) == 0 {
		recs = append(recs, Recommendation{
			ID:    "healthy",
			Title: "Prompting balance looks healthy. No anti-pattern flagged.",
		})
	}
	return recs
}
