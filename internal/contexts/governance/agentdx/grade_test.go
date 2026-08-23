package agentdx

import "testing"

// Metrics nobody grades are metrics nobody acts on. The wedge scorecard
// grades everything it reports; dx reported raw numbers and left the
// operator to decide whether 26% rework was good.
func TestGradesAreAssigned(t *testing.T) {
	g := Grade(Metrics{
		Prompts:                   100,
		Sessions:                  5,
		TotalEdits:                200,
		MedianTurnsPerPrompt:      4,
		P90TurnsPerPrompt:         9,
		ReworkRatePct:             3,
		InterruptRatePct:          1,
		FirstTryRatePct:           92,
		MedianSecondsPerPrompt:    45,
		MedianContextGrowthTokens: 4_000,
		CompactionsPerSession:     0.2,
	})
	for name, got := range map[string]Letter{
		"turns":     g.Turns,
		"rework":    g.Rework,
		"interrupt": g.Interrupt,
		"firstTry":  g.FirstTry,
		"duration":  g.Duration,
		"growth":    g.ContextGrowth,
		"compact":   g.Compaction,
	} {
		if got != LetterA {
			t.Errorf("%s = %q, want A for a healthy session profile", name, got)
		}
	}
	if g.Overall != LetterA {
		t.Errorf("Overall = %q, want A", g.Overall)
	}
}

// The maintainer's real profile should not grade as healthy — 11 turns a
// request with a quarter of edits redone is the thing worth surfacing.
func TestRealWorldProfileGradesPoorly(t *testing.T) {
	g := Grade(Metrics{
		Prompts:                   1119,
		Sessions:                  16,
		TotalEdits:                800,
		MedianTurnsPerPrompt:      11,
		P90TurnsPerPrompt:         84,
		ReworkRatePct:             26.2,
		InterruptRatePct:          0,
		FirstTryRatePct:           40,
		MedianContextGrowthTokens: 70_769,
		CompactionsPerSession:     1.3,
	})
	if g.Rework == LetterA || g.Rework == LetterB {
		t.Errorf("Rework = %q, want a poor grade at 26%%", g.Rework)
	}
	if g.Turns == LetterA {
		t.Errorf("Turns = %q, want worse than A at 11 median", g.Turns)
	}
	if g.Overall == LetterA {
		t.Errorf("Overall = %q, want worse than A", g.Overall)
	}
}

// A metric with no observations must not be graded — the whole point of
// the scorecard work was that an unmeasured value is not a zero.
func TestUnmeasuredMetricsAreNotGraded(t *testing.T) {
	g := Grade(Metrics{})
	if g.Turns != "" || g.Rework != "" || g.Overall != "" {
		t.Errorf("empty metrics must yield no grades: %+v", g)
	}
}

// Rework is only meaningful once some edits happened.
func TestReworkUngradedWithoutEdits(t *testing.T) {
	g := Grade(Metrics{Prompts: 50, MedianTurnsPerPrompt: 3, TotalEdits: 0})
	if g.Rework != "" {
		t.Errorf("Rework = %q, want ungraded with no edits", g.Rework)
	}
}
