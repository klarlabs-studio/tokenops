package agentdx

// Letter is a metric's grade. Empty means the metric was not measured —
// never a zero, never an F. A metric nobody observed is not a metric that
// scored badly, and conflating the two is how a scorecard ends up handing
// out perfect marks for absent data.
type Letter string

// Grades, best to worst.
const (
	LetterA Letter = "A"
	LetterB Letter = "B"
	LetterC Letter = "C"
	LetterF Letter = "F"
)

// Grades is the graded view of Metrics.
//
// Raw numbers leave the operator to decide whether 26% rework is good.
// Every other scorecard in the product grades what it reports; these did
// not, which made them a report rather than a signal.
type Grades struct {
	Turns         Letter `json:"turns,omitempty"`
	Duration      Letter `json:"duration,omitempty"`
	Rework        Letter `json:"rework,omitempty"`
	Interrupt     Letter `json:"interrupt,omitempty"`
	Escalation    Letter `json:"escalation,omitempty"`
	FirstTry      Letter `json:"first_try,omitempty"`
	ContextGrowth Letter `json:"context_growth,omitempty"`
	Compaction    Letter `json:"compaction,omitempty"`
	Overall       Letter `json:"overall,omitempty"`
	// OverallDriver names the metric Overall came from.
	//
	// Worst-grade-wins is deliberate, but it hands the headline to one
	// metric and never said which. An operator could watch rework improve
	// two grades while Overall sat unchanged, with nothing telling them
	// what was actually holding it there — so the rule looked arbitrary
	// when it was only unexplained.
	OverallDriver string `json:"overall_driver,omitempty"`
}

// Threshold is the A / B / C boundary for one metric. Anything past the
// C bound is an F.
type Threshold struct {
	Green, Yellow, Red float64
	// HigherIsBetter flips the comparison for metrics like first-try rate.
	HigherIsBetter bool
}

// DefaultThresholds are starting points, not laws.
//
// They are set where a difference changes what an operator would do, not
// at a statistical quantile: five turns for an instruction is a normal
// exchange, twenty is a session that got away. Rework is stricter than it
// looks — an agent revisiting its own edit one time in ten is already
// worth noticing, because the fix is usually a clearer instruction rather
// than a better model.
var DefaultThresholds = struct {
	Turns         Threshold
	Duration      Threshold
	Rework        Threshold
	Interrupt     Threshold
	Escalation    Threshold
	FirstTry      Threshold
	ContextGrowth Threshold
	Compaction    Threshold
}{
	Turns:         Threshold{Green: 5, Yellow: 12, Red: 25},
	Duration:      Threshold{Green: 120, Yellow: 600, Red: 1800},
	Rework:        Threshold{Green: 5, Yellow: 15, Red: 30},
	Interrupt:     Threshold{Green: 2, Yellow: 8, Red: 20},
	Escalation:    Threshold{Green: 40, Yellow: 70, Red: 90},
	FirstTry:      Threshold{Green: 80, Yellow: 60, Red: 40, HigherIsBetter: true},
	ContextGrowth: Threshold{Green: 10_000, Yellow: 40_000, Red: 100_000},
	Compaction:    Threshold{Green: 0.5, Yellow: 1.5, Red: 3},
}

// Grade scores metrics against DefaultThresholds. Metrics with no
// observations behind them are left ungraded.
func Grade(m Metrics) Grades {
	var g Grades
	if m.Prompts == 0 {
		return g
	}
	g.Turns = grade(m.MedianTurnsPerPrompt, DefaultThresholds.Turns)
	g.Interrupt = grade(m.InterruptRatePct, DefaultThresholds.Interrupt)
	g.Escalation = grade(m.EscalationRatePct, DefaultThresholds.Escalation)
	g.FirstTry = grade(m.FirstTryRatePct, DefaultThresholds.FirstTry)

	// Each of these needs its own observations, not just some prompts.
	// Grading a duration nobody timed would be the same mistake as
	// grading a latency nobody measured.
	if m.MedianSecondsPerPrompt > 0 {
		g.Duration = grade(m.MedianSecondsPerPrompt, DefaultThresholds.Duration)
	}
	if m.TotalEdits > 0 {
		g.Rework = grade(m.ReworkRatePct, DefaultThresholds.Rework)
	}
	if m.MedianContextGrowthTokens > 0 {
		g.ContextGrowth = grade(float64(m.MedianContextGrowthTokens), DefaultThresholds.ContextGrowth)
	}
	if m.Sessions > 0 {
		g.Compaction = grade(m.CompactionsPerSession, DefaultThresholds.Compaction)
	}
	g.Overall, g.OverallDriver = worstWithDriver(g)
	return g
}

func grade(v float64, t Threshold) Letter {
	if t.HigherIsBetter {
		switch {
		case v >= t.Green:
			return LetterA
		case v >= t.Yellow:
			return LetterB
		case v >= t.Red:
			return LetterC
		default:
			return LetterF
		}
	}
	switch {
	case v <= t.Green:
		return LetterA
	case v <= t.Yellow:
		return LetterB
	case v <= t.Red:
		return LetterC
	default:
		return LetterF
	}
}

// worstWithDriver returns the lowest grade present and the metric it came
// from.
//
// The worst rather than the average, deliberately: an experience is as
// good as its sharpest friction, and averaging lets one bad metric hide
// behind six comfortable ones. The driver is reported alongside because
// that rule only reads as fair when the operator can see which metric it
// picked — otherwise a headline that refuses to move looks arbitrary
// rather than pointed.
//
// A tie goes to the first in declaration order, so repeated runs over the
// same data name the same metric.
func worstWithDriver(g Grades) (Letter, string) {
	rank := map[Letter]int{LetterA: 0, LetterB: 1, LetterC: 2, LetterF: 3}
	out := Letter("")
	driver := ""
	best := -1
	for _, m := range []struct {
		name   string
		letter Letter
	}{
		{"turns-per-prompt", g.Turns},
		{"duration", g.Duration},
		{"rework", g.Rework},
		{"interrupt rate", g.Interrupt},
		{"escalation rate", g.Escalation},
		{"first-try rate", g.FirstTry},
		{"context growth", g.ContextGrowth},
		{"compactions", g.Compaction},
	} {
		l := m.letter
		if l == "" {
			continue
		}
		if r := rank[l]; r > best {
			best, out, driver = r, l, m.name
		}
	}
	return out, driver
}
