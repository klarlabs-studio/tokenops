package replies

import (
	"regexp"
	"sort"
	"strings"
)

// Findings is the analyzer output for `tokenops coach replies`. It
// reports both global statistics and per-session breakdowns so the
// operator can see which sessions ran in compressed-output mode (e.g.
// caveman skill engaged) vs. baseline verbose mode.
type Findings struct {
	TotalReplies int           `json:"total_replies"`
	Baseline     Stats         `json:"baseline"`
	BySession    []SessionStat `json:"by_session"`
	// CavemanLikely counts sessions whose pattern strongly suggests an
	// output-compression skill is engaged (article-density << baseline
	// AND filler-density near zero AND short avg word length).
	CavemanLikelySessions int `json:"caveman_likely_sessions"`
	// EstimatedTokenSavings is a rough lower-bound estimate of output
	// tokens that would have been emitted without compression. Computed
	// per-session and summed. Useful as a TEU input for the scorecard.
	EstimatedTokenSavings int `json:"estimated_token_savings"`
}

// Stats is the per-session or aggregate measurement bundle.
type Stats struct {
	Replies        int     `json:"replies"`
	AvgWords       float64 `json:"avg_words"`
	AvgWordLen     float64 `json:"avg_word_len"`
	ArticleRatio   float64 `json:"article_ratio"` // (a/an/the) / words
	FillerRatio    float64 `json:"filler_ratio"`  // hedging+pleasantry / words
	CodeBlockRatio float64 `json:"code_block_ratio"`
}

// SessionStat is per-session stats plus the verdict.
type SessionStat struct {
	SessionID            string `json:"session_id"`
	Stats                Stats  `json:"stats"`
	CavemanLikely        bool   `json:"caveman_likely"`
	EstimatedSavedTokens int    `json:"estimated_saved_tokens"`
}

// detection thresholds (tuned conservatively; bias toward false-negative
// rather than false-positive so operators don't get "caveman engaged"
// on every terse reply).
const (
	cavemanArticleMax    = 0.025 // baseline English ~7%; <2.5% is suspicious
	cavemanFillerMax     = 0.005 // baseline ~1%; <0.5% is suspicious
	cavemanAvgWordLenMax = 5.0   // caveman uses short synonyms (baseline ~5.1)
	cavemanMinReplies    = 3     // single replies are too noisy to flag
)

// Token-savings heuristic: assume baseline English would have used ~7%
// articles + ~1% filler + a 10% pleasantry/hedging overhead. The
// difference vs the session's measured ratios estimates suppressed
// tokens.
const baselineOverheadRatio = 0.15 // 15% baseline overhead

// Analyze rolls a slice of replies into Findings. Empty input returns
// a zero Findings with no error.
func Analyze(replies []AssistantReply) Findings {
	var f Findings
	if len(replies) == 0 {
		return f
	}
	f.TotalReplies = len(replies)

	// First pass: global aggregate.
	f.Baseline = statsFor(replies)

	// Second pass: per-session.
	bySession := map[string][]AssistantReply{}
	for _, r := range replies {
		bySession[r.SessionID] = append(bySession[r.SessionID], r)
	}
	for sid, sr := range bySession {
		s := statsFor(sr)
		entry := SessionStat{SessionID: sid, Stats: s}
		// Verdict: replies must hit minimum sample size; then the strong
		// signals are article + filler suppression (the caveman skill
		// explicitly drops both). Average word length is a weak signal
		// that's easily confounded by technical jargon — we record it
		// but do not gate on it.
		if s.Replies >= cavemanMinReplies &&
			s.ArticleRatio < cavemanArticleMax &&
			s.FillerRatio < cavemanFillerMax {
			entry.CavemanLikely = true
			f.CavemanLikelySessions++
		}
		// Estimated savings: token cost of the compressed replies times
		// (baselineOverheadRatio / (1 - baselineOverheadRatio)). Only
		// when the session looks compressed.
		if entry.CavemanLikely {
			totalWords := int(s.AvgWords * float64(s.Replies))
			// Approximate tokens ≈ words * 1.3.
			emittedTokens := int(float64(totalWords) * 1.3)
			saved := int(float64(emittedTokens) * (baselineOverheadRatio / (1 - baselineOverheadRatio)))
			entry.EstimatedSavedTokens = saved
			f.EstimatedTokenSavings += saved
		}
		f.BySession = append(f.BySession, entry)
	}
	sort.Slice(f.BySession, func(i, j int) bool {
		// Caveman sessions first, then by reply count desc.
		if f.BySession[i].CavemanLikely != f.BySession[j].CavemanLikely {
			return f.BySession[i].CavemanLikely
		}
		return f.BySession[i].Stats.Replies > f.BySession[j].Stats.Replies
	})
	return f
}

// statsFor computes the per-reply averages over a set of replies.
func statsFor(rs []AssistantReply) Stats {
	if len(rs) == 0 {
		return Stats{}
	}
	var (
		totalWords      int
		totalWordChars  int
		articles        int
		fillers         int
		repliesWithCode int
	)
	for _, r := range rs {
		// Strip fenced code blocks from prose-density math; they
		// don't reflect compression style. Track presence separately.
		prose, hadCode := splitCodeBlocks(r.Text)
		if hadCode {
			repliesWithCode++
		}
		words := tokenize(prose)
		totalWords += len(words)
		for _, w := range words {
			lw := strings.ToLower(w)
			totalWordChars += len(lw)
			if _, ok := articleSet[lw]; ok {
				articles++
			}
			if _, ok := fillerSet[lw]; ok {
				fillers++
			}
		}
	}
	if totalWords == 0 {
		return Stats{Replies: len(rs)}
	}
	return Stats{
		Replies:        len(rs),
		AvgWords:       float64(totalWords) / float64(len(rs)),
		AvgWordLen:     float64(totalWordChars) / float64(totalWords),
		ArticleRatio:   float64(articles) / float64(totalWords),
		FillerRatio:    float64(fillers) / float64(totalWords),
		CodeBlockRatio: float64(repliesWithCode) / float64(len(rs)),
	}
}

var (
	wordRe = regexp.MustCompile(`[A-Za-z][A-Za-z'-]*`)

	articleSet = map[string]struct{}{
		"a": {}, "an": {}, "the": {},
	}
	// Filler / pleasantry / hedging words the caveman skill explicitly
	// instructs the model to drop. Matching any of these in volume
	// signals the model is NOT in caveman mode.
	fillerSet = map[string]struct{}{
		"just": {}, "really": {}, "basically": {}, "actually": {},
		"simply": {}, "very": {}, "quite": {}, "rather": {},
		"sure": {}, "certainly": {}, "absolutely": {}, "definitely": {},
		"happy": {}, "glad": {}, "pleased": {},
		"perhaps": {}, "maybe": {}, "possibly": {},
		"essentially": {}, "fundamentally": {},
	}
)

func tokenize(s string) []string {
	return wordRe.FindAllString(s, -1)
}

// splitCodeBlocks returns the prose with fenced ``` blocks removed
// (those skew word stats) and a boolean indicating whether the
// original text contained any fenced block.
func splitCodeBlocks(s string) (string, bool) {
	if !strings.Contains(s, "```") {
		return s, false
	}
	parts := strings.Split(s, "```")
	// even indices = outside fences, odd indices = inside.
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 0 {
			b.WriteString(p)
			b.WriteByte(' ')
		}
	}
	return b.String(), true
}
