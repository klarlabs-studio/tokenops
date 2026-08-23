package agentdx

import "fmt"

// Recommendation is the single change most likely to improve the
// experience, chosen from whichever metric grades worst.
//
// A graded report still leaves the operator to work out what to do about
// a C. This names it, in the same shape the prompt coach uses: the
// finding, the evidence it rests on, and the action.
type Recommendation struct {
	Title    string
	Evidence string
	Action   string
}

// Recommend returns the highest-leverage change for these metrics, or
// reports that nothing stands out.
//
// It picks one thing rather than listing everything. A list of eight
// findings is a report; one named change is advice, and an operator can
// only act on so much at once.
func Recommend(m Metrics) (Recommendation, bool) {
	g := Grade(m)
	if g.Overall == "" || g.Overall == LetterA {
		return Recommendation{}, false
	}

	// Ordered by leverage, not severity: rework and interrupts are things
	// a clearer instruction fixes today, while turn count is usually a
	// symptom of them rather than a cause.
	switch {
	case poor(g.Rework) && m.TotalEdits > 0:
		return Recommendation{
			Title: "The agent is redoing its own edits",
			Evidence: fmt.Sprintf(
				"%.1f%% of edits revisit a file already edited answering the same instruction, across %d edits",
				m.ReworkRatePct, m.TotalEdits),
			Action: "state the whole change up front — which files, which cases — so the first attempt has what the second one needed",
		}, true

	case poor(g.Interrupt):
		return Recommendation{
			Title:    "You are having to stop the agent",
			Evidence: fmt.Sprintf("%.1f%% of instructions were interrupted", m.InterruptRatePct),
			Action:   "say what NOT to do in the instruction; an interrupt is usually a scope you did not rule out",
		}, true

	case poor(g.ContextGrowth):
		return Recommendation{
			Title: "Context grows fast between turns",
			Evidence: fmt.Sprintf(
				"%d tokens added per turn (median), %.1f compactions per session",
				m.MedianContextGrowthTokens, m.CompactionsPerSession),
			Action: "narrow what each turn pulls in — ranged reads over whole files, and `tokenops fmt` on the noisiest commands",
		}, true

	case poor(g.Turns):
		return Recommendation{
			Title: "Instructions take a lot of turns",
			Evidence: fmt.Sprintf(
				"%.1f turns for a typical instruction, %.1f at p90",
				m.MedianTurnsPerPrompt, m.P90TurnsPerPrompt),
			Action: "split the long ones — a p90 far above the median usually means a few instructions are really several",
		}, true

	case poor(g.FirstTry):
		return Recommendation{
			Title:    "Few instructions land first time",
			Evidence: fmt.Sprintf("%.1f%% completed with no rework, interrupt, or delegation", m.FirstTryRatePct),
			Action:   "add the acceptance criterion to the instruction — what does done look like",
		}, true
	}
	return Recommendation{}, false
}

// poor reports whether a grade is worth acting on.
func poor(l Letter) bool { return l == LetterC || l == LetterF }
