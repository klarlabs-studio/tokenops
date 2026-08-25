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
	// RepeatCallRatePct is the share of tool calls the agent had already
	// made, with identical arguments, within the recent lookback. It is
	// the agent losing track of what it has already done — the drift an
	// operator reads as declining quality without ever saying so.
	RepeatCallRatePct float64 `json:"repeat_call_rate_pct"`
	RepeatCalls       int     `json:"repeat_calls"`
	TotalCalls        int     `json:"total_calls"`
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

// repeatLookback bounds how far back a repeated call is recognised.
//
// The bound is the whole point. With unbounded history the repeat rate
// measures session length — later calls have more prior calls to collide
// with — and on real transcripts that confound is large enough to
// manufacture a trend on its own. Fifty calls is roughly the span an
// agent can be expected to remember it just did something.
const repeatLookback = 50

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
		prompts, rejects   int
		turns              []float64
		rework, edits      int
		repeats, callsSeen int
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
		// Bounded lookback over recent call signatures, kept PER SESSION.
		//
		// Bounded, because unbounded history makes the repeat rate a proxy
		// for session length: the longer a session runs, the more prior
		// calls there are to collide with, drift or no drift.
		//
		// Per session, because an agent cannot repeat itself across a
		// boundary it has no memory of. Sharing one window across sessions
		// counts two operators running the same command as the agent
		// forgetting — which on real transcripts is enough to invert the
		// trend entirely.
		lookback = map[string]*callWindow{}
	)
	sawCall := func(sessionID, sig string) bool {
		if sig == "" {
			return false
		}
		w := lookback[sessionID]
		if w == nil {
			w = newCallWindow()
			lookback[sessionID] = w
		}
		return w.seen(sig)
	}
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
			idx := bandIndex(curContext)
			if idx >= 0 && r.CallSignature != "" {
				accs[idx].callsSeen++
				if sawCall(r.SessionID, r.CallSignature) {
					accs[idx].repeats++
				}
			}
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
		// A band is worth reporting if anything happened in it. Repeats
		// are counted per call, so a band can carry a real signal without
		// any instruction having started inside it.
		if a.prompts == 0 && a.callsSeen == 0 {
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
		band.RepeatCalls, band.TotalCalls = a.repeats, a.callsSeen
		if a.callsSeen > 0 {
			band.RepeatCallRatePct = round1(float64(a.repeats) / float64(a.callsSeen) * 100)
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

// DegradationNote reports what the curve says about working at a large
// context — including, often, that it says nothing.
//
// Repeats lead, because on real transcripts they are the signal that
// shows the effect and rejection is not. An operator rarely tells the
// agent off for redoing something; they just watch it happen and form an
// impression. That impression is what this names.
//
// Bands are compared as two halves rather than best-versus-worst. Picking
// the worst band cherry-picks a peak out of a noisy series: the same
// corpus that shows a real repeat effect also shows a rejection series of
// 3.7 / 1.9 / 3.3 / 2.4 / 1.7, where worst-versus-best reads as a
// confident degradation and the trend is flat.
func DegradationNote(bands []ContextBand) string {
	loRate, loN, hiRate, hiN := splitHalves(bands)
	if loN == 0 || hiN == 0 {
		return ""
	}
	if loRate <= 0 {
		return ""
	}
	rise := (hiRate/loRate - 1) * 100
	if rise < minRepeatRisePct {
		return fmt.Sprintf(
			"No clear drift: the agent re-issues a recent call %.1f%% of the time below %s "+
				"and %.1f%% above — inside the noise for this corpus.",
			loRate, splitLabel, hiRate)
	}
	return fmt.Sprintf(
		"Past %s the agent re-issues a call it just made %.0f%% more often (%.1f%% vs %.1f%%) — "+
			"it is losing track of what it has already done. Compacting before %s keeps it in "+
			"the band where it does not.",
		splitLabel, rise, hiRate, loRate, splitLabel)
}

// splitLabel and splitAt are where the halves divide. Chosen because it
// is where the effect appears on real transcripts, not for roundness.
const (
	splitLabel       = "600k"
	splitAt    int64 = 600_000
	// minRepeatRisePct is the relative increase below which the halves
	// are treated as the same. These are rates over tens of thousands of
	// calls, but a tool that names small differences as findings teaches
	// its operator to distrust the ones that matter.
	minRepeatRisePct = 25.0
)

// splitHalves aggregates repeat rates below and at/above splitAt.
func splitHalves(bands []ContextBand) (loRate float64, loN int, hiRate float64, hiN int) {
	var loDup, hiDup int
	for _, b := range bands {
		if b.TotalCalls == 0 {
			continue
		}
		if b.Lo < splitAt {
			loN += b.TotalCalls
			loDup += b.RepeatCalls
		} else {
			hiN += b.TotalCalls
			hiDup += b.RepeatCalls
		}
	}
	if loN > 0 {
		loRate = round1(float64(loDup) / float64(loN) * 100)
	}
	if hiN > 0 {
		hiRate = round1(float64(hiDup) / float64(hiN) * 100)
	}
	return loRate, loN, hiRate, hiN
}

// callWindow is a fixed-size ring of recent call signatures for one
// session.
type callWindow struct {
	order []string
	count map[string]int
}

func newCallWindow() *callWindow {
	return &callWindow{order: make([]string, 0, repeatLookback), count: map[string]int{}}
}

// seen records sig and reports whether it was already inside the window.
func (w *callWindow) seen(sig string) bool {
	dup := w.count[sig] > 0
	if len(w.order) == repeatLookback {
		old := w.order[0]
		w.order = w.order[1:]
		if w.count[old]--; w.count[old] <= 0 {
			delete(w.count, old)
		}
	}
	w.order = append(w.order, sig)
	w.count[sig]++
	return dup
}
