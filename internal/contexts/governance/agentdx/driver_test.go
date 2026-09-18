package agentdx

import "testing"

// Overall is the worst grade by design, but it never said which metric set
// it. An operator watching one metric improve two grades while Overall sat
// still had nothing telling them what was actually holding it there.
func TestOverallNamesTheMetricThatSetIt(t *testing.T) {
	g := Grades{
		Turns:      LetterA,
		Rework:     LetterF,
		Escalation: LetterB,
	}
	g.Overall, g.OverallDriver = worstWithDriver(g)
	if g.Overall != LetterF {
		t.Fatalf("Overall = %q, want F", g.Overall)
	}
	if g.OverallDriver != "rework" {
		t.Fatalf("OverallDriver = %q, want rework", g.OverallDriver)
	}
}

// Ties go to the first in declaration order, so the answer is stable across
// runs rather than depending on map iteration.
func TestDriverIsStableOnATie(t *testing.T) {
	g := Grades{Turns: LetterC, Rework: LetterC}
	for i := 0; i < 20; i++ {
		_, driver := worstWithDriver(g)
		if driver != "turns-per-prompt" {
			t.Fatalf("iteration %d: driver = %q, want the first declared", i, driver)
		}
	}
}

// Nothing graded means nothing to name.
func TestNoDriverWhenNothingIsGraded(t *testing.T) {
	letter, driver := worstWithDriver(Grades{})
	if letter != "" || driver != "" {
		t.Fatalf("got (%q, %q), want empty", letter, driver)
	}
}

// An all-A run still names its driver: "the worst" is still a real metric,
// and seeing which one is closest to the edge is the point.
func TestDriverNamedEvenWhenEverythingIsAnA(t *testing.T) {
	letter, driver := worstWithDriver(Grades{Turns: LetterA, Rework: LetterA})
	if letter != LetterA {
		t.Fatalf("Overall = %q, want A", letter)
	}
	if driver == "" {
		t.Fatal("want the driver named even at A")
	}
}
