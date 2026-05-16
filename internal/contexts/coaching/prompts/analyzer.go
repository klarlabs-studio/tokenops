package prompts

import (
	"sort"
	"strings"
)

// Findings is the analyzer's report: aggregate stats + flagged
// patterns + a short recommendation list. Designed for JSON
// rendering (CLI --json, MCP tool result) so the field names are the
// rendering contract — don't rename without bumping the MCP tool.
type Findings struct {
	TotalPrompts       int            `json:"total_prompts"`
	AvgChars           float64        `json:"avg_chars"`
	AvgWords           float64        `json:"avg_words"`
	MinChars           int            `json:"min_chars"`
	MaxChars           int            `json:"max_chars"`
	LengthDistribution map[string]int `json:"length_distribution"`
	VagueShort         int            `json:"vague_short_count"`
	VagueShortSamples  []string       `json:"vague_short_samples,omitempty"`
	Acknowledgements   int            `json:"acknowledgement_count"`
	ShortQuestions     int            `json:"short_question_count"`
	NoContextSingles   int            `json:"no_context_singles_count"`
	RepeatedPrompts    []RepeatedItem `json:"repeated_prompts,omitempty"`
	Recommendations    []string       `json:"recommendations"`
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
	"yes please": {}, "do it": {}, "go ahead": {},
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
		f.Recommendations = []string{"no user prompts in window — nothing to analyze"}
		return f
	}
	f.TotalPrompts = len(prompts)

	var charSum, wordSum int
	f.MinChars = len(prompts[0].Text)
	repeatCounts := map[string]int{}

	var (
		vagueSamples []string
		acks         int
		shortQs      int
		noCtx        int
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

	f.Recommendations = recommendations(f)
	return f
}

// recommendations turns a Findings rollup into concrete advice. The
// thresholds match the cutoffs in the public-facing copy so an
// operator reading the percentages can predict which lines fire.
func recommendations(f Findings) []string {
	var recs []string
	if f.TotalPrompts == 0 {
		return []string{"no data"}
	}
	ackRatio := float64(f.Acknowledgements) / float64(f.TotalPrompts)
	if ackRatio > 0.15 {
		recs = append(recs, "Reduce confirmation loops: give upfront briefs that cover scope, constraints, and success criteria so the agent doesn't need to ask 'yes/no' so often.")
	}
	shortRatio := float64(f.LengthDistribution["<5w"]) / float64(f.TotalPrompts)
	if shortRatio > 0.20 {
		recs = append(recs, "Front-load context: a large share of your prompts are under 5 words. Agents perform better with 15-50 word turns that cite the file, the goal, and the constraint.")
	}
	if len(f.RepeatedPrompts) > 0 {
		recs = append(recs, "You repeat the same prompt verbatim 3+ times in some sessions. When the agent doesn't act, switch to 'do not ask, just do X' rather than re-issuing the same instruction.")
	}
	if f.AvgWords < 10 && f.TotalPrompts > 20 {
		recs = append(recs, "Avg prompt is very short. Strong prompts cite file paths, line numbers, and expected output format — they cost a few extra tokens but cut iterations.")
	}
	if len(recs) == 0 {
		recs = append(recs, "Prompting balance looks healthy. No anti-pattern flagged.")
	}
	return recs
}
