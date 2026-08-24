package agentdx

import "fmt"

// ContextBand is one slice of the context-size range, with the friction
// measured while the window was that full.
type ContextBand struct {
	Label string `json:"label"`
	// Lo and Hi bound the band in tokens; Hi of 0 means unbounded.
	Lo int64 `json:"lo"`
	Hi int64 `json:"hi"`
	// Prompts is how many instructions ran at this context size.
	Prompts int `json:"prompts"`
	// RejectRatePct is the share of instructions in which the operator
	// rejected what they had just been given.
	RejectRatePct float64 `json:"reject_rate_pct"`
	// MedianTurns and ReworkRatePct are reported alongside, but they
	// answer a different question — see the note on ComputeContextCurve.
	MedianTurns   float64 `json:"median_turns"`
	ReworkRatePct float64 `json:"rework_rate_pct"`
	TotalEdits    int     `json:"total_edits"`
}

// Contains reports whether a context size falls in this band.
func (b ContextBand) Contains(tokens int64) bool {
	return tokens >= b.Lo && (b.Hi == 0 || tokens < b.Hi)
}

// contextBounds are the band edges, chosen to be legible rather than
// statistically derived: an operator thinks in "a couple of hundred k",
// not in quantiles of their own distribution.
var contextBounds = []struct {
	label  string
	lo, hi int64
}{
	{"<200k", 0, 200_000},
	{"200-400k", 200_000, 400_000},
	{"400-600k", 400_000, 600_000},
	{"600-800k", 600_000, 800_000},
	{">800k", 800_000, 0},
}

// minPromptsPerBand is the floor for a band to be trusted. Below it a
// percentage swings on single events.
const minPromptsPerBand = 50

// ComputeContextCurve measures friction against how full the context
// window was.
//
// The headline signal is the rejection rate: the operator read what came
// back and said it was wrong. That is the closest thing to a quality
// verdict a transcript holds.
//
// Turns and rework are reported beside it but should not be read the same
// way. Both are confounded by session position — context grows
// monotonically within a session, so a low-context instruction is
// usually an early one, when the agent is still orienting. On the
// maintainer's own corpus that confound is strong enough to invert them:
// rework FALLS from 49.8% under 200k to 32.9% above 800k, and tool errors
// fall likewise, which says more about session warmup than about context.
//
// Rejection resists that confound better, because it is a judgement made
// about the turn just received rather than a property of the session so
// far. It is still not proof: on the maintainer's own corpus it runs
// 3.7 / 1.9 / 3.1 / 2.4 / 1.7 across the bands — noisy, and if anything
// improving with context rather than degrading.
//
// That result is worth stating plainly, because the intuition it fails to
// confirm is widely held and a subsample appears to support it. Measuring
// the forty largest transcripts alone produces a clean U with a minimum at
// 400-600k; the full corpus does not reproduce it. This function exists so
// an operator can check the claim against their own data instead of
// inheriting anyone's assumption about it — including the assumption that
// the effect is there.
func ComputeContextCurve(records []Record) []ContextBand {
	if len(records) == 0 {
		return nil
	}
	type acc struct {
		prompts, rejects int
		turns            []float64
		rework, edits    int
	}
	accs := make([]acc, len(contextBounds))

	bandIndex := func(tokens int64) int {
		for i, b := range contextBounds {
			if tokens >= b.lo && (b.hi == 0 || tokens < b.hi) {
				return i
			}
		}
		return -1
	}

	// Walk in order, carrying the context size the session had reached so
	// each instruction is attributed to the window it actually ran in.
	var (
		curContext int64
		unit       *acc
		unitTurns  int
		unitFiles  map[string]bool
	)
	closeUnit := func() {
		if unit == nil {
			return
		}
		unit.turns = append(unit.turns, float64(unitTurns))
		unit = nil
	}

	for _, r := range sortedByTime(records) {
		switch r.Kind {
		case KindAssistantTurn:
			if r.InputTokens > 0 {
				curContext = r.InputTokens
			}
			if unit != nil {
				unitTurns++
			}
		case KindPrompt:
			closeUnit()
			idx := bandIndex(curContext)
			if idx < 0 {
				continue
			}
			unit = &accs[idx]
			unitTurns = 0
			unitFiles = map[string]bool{}
			unit.prompts++
			if r.Rejects {
				unit.rejects++
			}
		case KindToolUse:
			if unit == nil || !isEdit(r.ToolName) || r.FilePath == "" {
				continue
			}
			unit.edits++
			if unitFiles[r.FilePath] {
				unit.rework++
			}
			unitFiles[r.FilePath] = true
		}
	}
	closeUnit()

	out := make([]ContextBand, 0, len(contextBounds))
	for i, b := range contextBounds {
		a := accs[i]
		if a.prompts == 0 {
			continue
		}
		band := ContextBand{
			Label: b.label, Lo: b.lo, Hi: b.hi,
			Prompts:     a.prompts,
			MedianTurns: round1(percentile(a.turns, 0.5)),
			TotalEdits:  a.edits,
		}
		band.RejectRatePct = round1(float64(a.rejects) / float64(a.prompts) * 100)
		if a.edits > 0 {
			band.ReworkRatePct = round1(float64(a.rework) / float64(a.edits) * 100)
		}
		out = append(out, band)
	}
	return out
}

// SweetSpot returns the band where the operator rejected least, among
// those with enough observations to mean anything.
//
// This is the point of the curve: a compaction threshold drawn from the
// operator's own measured best, rather than a round number someone chose.
func SweetSpot(bands []ContextBand) (ContextBand, bool) {
	var (
		best  ContextBand
		found bool
	)
	for _, b := range bands {
		if b.Prompts < minPromptsPerBand {
			continue
		}
		if !found || b.RejectRatePct < best.RejectRatePct {
			best, found = b, true
		}
	}
	return best, found
}

// DegradationNote reports what the curve says about working past the
// sweet spot — including, often, that it says nothing.
//
// It compares the LARGEST populated band against the sweet spot, not the
// worst one. Picking the worst band cherry-picks a peak out of a noisy
// series: on the maintainer's own corpus rejection runs 3.7 / 1.9 / 3.1 /
// 2.4 / 1.7 across the bands, where "worst vs best" reads as a 93%
// degradation and the actual trend is flat-to-improving.
//
// A margin is required too. These are percentages of a few hundred
// prompts; a fraction of a point apart is noise, and a tool that names it
// as a finding teaches its operator to distrust the ones that are real.
func DegradationNote(bands []ContextBand) string {
	spot, ok := SweetSpot(bands)
	if !ok {
		return ""
	}
	var largest ContextBand
	var haveLargest bool
	for _, b := range bands {
		if b.Prompts < minPromptsPerBand {
			continue
		}
		if !haveLargest || b.Lo > largest.Lo {
			largest, haveLargest = b, true
		}
	}
	if !haveLargest || largest.Label == spot.Label {
		return fmt.Sprintf(
			"Your lowest rejection rate is at %s (%.1f%%), and it is also your largest "+
				"measured band — no degradation with context in this data.",
			spot.Label, spot.RejectRatePct)
	}
	rise := largest.RejectRatePct - spot.RejectRatePct
	if rise < minMeaningfulRise {
		return fmt.Sprintf(
			"No clear degradation: %s runs %.1f%% against %.1f%% at your best band (%s), "+
				"which is inside the noise for this many prompts.",
			largest.Label, largest.RejectRatePct, spot.RejectRatePct, spot.Label)
	}
	pct := rise / spot.RejectRatePct * 100
	return fmt.Sprintf(
		"You reject %.0f%% more often at %s than at %s (%.1f%% vs %.1f%%) — compacting "+
			"before %s keeps you in your own best band.",
		pct, largest.Label, spot.Label, largest.RejectRatePct, spot.RejectRatePct, largest.Label)
}

// minMeaningfulRise is the percentage-point gap below which a difference
// between bands is treated as noise rather than a finding.
const minMeaningfulRise = 1.0
