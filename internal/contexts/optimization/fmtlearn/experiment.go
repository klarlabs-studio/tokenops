package fmtlearn

import (
	"fmt"
	"hash/fnv"
	"math"
)

// The fmt outcome experiment. Bytes saved say what fmt removed, not
// whether the agent still found what it needed: a compression that drops
// the line the agent wanted shows up later as the command run again, a
// file reworked, or a task that failed. So when the experiment is on, a
// share of sessions, drawn at random, get their command output raw, and
// the sessions in each arm are compared on how the work went. Squeez
// (arXiv 2604.04979) argues for judging compression by what the agent
// still finds; this is that, on the operator's own work.

// The experiment's arms, as a compress record names them.
const (
	// ArmFmt is a session whose command output fmt compressed.
	ArmFmt = "fmt"
	// ArmRaw is a session whose command output fmt passed through
	// unchanged, recording what it would have saved.
	ArmRaw = "raw"
)

// The raw arm's bounds. Half the sessions is the most a comparison
// gains from; past it the experiment withholds compression for no
// better evidence.
const (
	DefaultExperimentRawPercent = 50
	MaxExperimentRawPercent     = 50
)

// ExperimentArm assigns a session to an arm: raw with probability
// rawPercent/100 (clamped to MaxExperimentRawPercent), else fmt. It
// hashes the session id alone, so every command in a session lands in
// the same arm and nothing the outcome could depend on decides it. It
// returns "" when the experiment is off or there is no session to
// assign: a run nobody can join to an outcome stays out of it.
func ExperimentArm(session string, rawPercent int) string {
	if session == "" || rawPercent <= 0 {
		return ""
	}
	rawPercent = min(rawPercent, MaxExperimentRawPercent)
	h := fnv.New32a()
	_, _ = h.Write([]byte("fmt-experiment|" + session))
	if int(h.Sum32()%100) < rawPercent {
		return ArmRaw
	}
	return ArmFmt
}

// SessionOutcome is one session in the experiment: its arm, what fmt did
// or would have done in it, and how the work went.
type SessionOutcome struct {
	Session string `json:"session"`
	Arm     string `json:"arm"`
	// Runs counts the session's wrapped commands; TokensSaved what fmt
	// saved in them, or in the raw arm what it would have saved.
	Runs        int   `json:"runs"`
	TokensSaved int64 `json:"tokens_saved"`
	// Instructions counts the session's instruction units, FirstTry
	// those done without rework, interrupt or delegation, Reworked those
	// that returned to a file they had moved on from.
	Instructions int `json:"instructions"`
	FirstTry     int `json:"first_try"`
	Reworked     int `json:"reworked"`
	// TokensPerInstruction is the median context an instruction took,
	// summed across its turns.
	TokensPerInstruction int64 `json:"tokens_per_instruction"`
	// ToolCalls counts the tool calls whose arguments were recorded;
	// RepeatCalls those the session had already made with the same
	// arguments, such as a command run again.
	ToolCalls   int `json:"tool_calls"`
	RepeatCalls int `json:"repeat_calls"`
	// Outcome is the recorded result for the session (achieved, partial,
	// not_achieved), or empty when nobody recorded one.
	Outcome string `json:"outcome,omitempty"`
}

// The noise rule. A comparison needs this many sessions and instructions
// in each arm before anything is read from it, the same floor
// moveverdict's session policy uses: instructions in one session are not
// independent, so one bad afternoon must not read as an effect.
const (
	MinExperimentSessions     = 4
	MinExperimentInstructions = 20
)

// ArmSummary is one arm's counts.
type ArmSummary struct {
	Arm          string `json:"arm"`
	Sessions     int    `json:"sessions"`
	Instructions int    `json:"instructions"`
	Runs         int    `json:"runs"`
	// TokensSaved is what fmt saved in the fmt arm and would have saved
	// in the raw arm.
	TokensSaved int64 `json:"tokens_saved"`
	// Assessed counts the sessions with a recorded outcome.
	Assessed int `json:"assessed"`
}

// Measure is one outcome compared between the arms.
type Measure struct {
	Name string `json:"name"`
	// Unit is what the figures are in: "%" or "tokens".
	Unit string `json:"unit"`
	// HigherIsBetter says which way is good.
	HigherIsBetter bool `json:"higher_is_better"`
	// Fmt and Raw are the mean of the per-session values in each arm,
	// over FmtSessions and RawSessions sessions that had one.
	Fmt         float64 `json:"fmt"`
	Raw         float64 `json:"raw"`
	FmtSessions int     `json:"fmt_sessions"`
	RawSessions int     `json:"raw_sessions"`
	// Diff is Fmt minus Raw. Low and High are a rough 95% interval on
	// it, computed over sessions; Interval says whether they were.
	Diff     float64 `json:"diff"`
	Low      float64 `json:"low,omitempty"`
	High     float64 `json:"high,omitempty"`
	Interval bool    `json:"interval"`
	// Reading says what the comparison shows, in words.
	Reading string `json:"reading"`
}

// ExperimentReport compares the arms.
type ExperimentReport struct {
	RawPercent int        `json:"raw_percent"`
	Fmt        ArmSummary `json:"fmt"`
	Raw        ArmSummary `json:"raw"`
	// Mixed counts sessions with commands in both arms, as happens when
	// the share changed mid-session. They are left out.
	Mixed    int       `json:"mixed,omitempty"`
	Measures []Measure `json:"measures"`
	// Enough says whether both arms passed the noise rule; Why says what
	// is missing when they did not.
	Enough bool   `json:"enough"`
	Why    string `json:"why,omitempty"`
}

// CompareArms compares the sessions of each arm on outcomes. It is pure.
//
// Every measure is computed per session and compared over sessions,
// because the session is what was randomized: pooling instructions
// would count one long session's habits as many independent results.
// The interval is a normal approximation and is called rough; with
// several measures one may exclude zero by chance, which the reading
// says rather than calling it a finding.
func CompareArms(sessions []SessionOutcome, rawPercent int) ExperimentReport {
	r := ExperimentReport{RawPercent: rawPercent, Fmt: ArmSummary{Arm: ArmFmt}, Raw: ArmSummary{Arm: ArmRaw}}
	var fmtS, rawS []SessionOutcome
	for _, s := range sessions {
		var sum *ArmSummary
		switch s.Arm {
		case ArmFmt:
			fmtS, sum = append(fmtS, s), &r.Fmt
		case ArmRaw:
			rawS, sum = append(rawS, s), &r.Raw
		default:
			continue
		}
		sum.Sessions++
		sum.Instructions += s.Instructions
		sum.Runs += s.Runs
		sum.TokensSaved += s.TokensSaved
		if outcomeScore(s.Outcome) >= 0 {
			sum.Assessed++
		}
	}

	r.Enough = r.Fmt.Sessions >= MinExperimentSessions && r.Raw.Sessions >= MinExperimentSessions &&
		r.Fmt.Instructions >= MinExperimentInstructions && r.Raw.Instructions >= MinExperimentInstructions
	if !r.Enough {
		r.Why = fmt.Sprintf("not enough sessions yet: fmt %d (%d instructions), raw %d (%d instructions); each arm needs %d sessions and %d instructions",
			r.Fmt.Sessions, r.Fmt.Instructions, r.Raw.Sessions, r.Raw.Instructions, MinExperimentSessions, MinExperimentInstructions)
	}
	for _, d := range measureDefs {
		r.Measures = append(r.Measures, compare(d, fmtS, rawS, r.Enough, len(measureDefs)))
	}
	return r
}

// measureDef is how one measure reads a session; ok is false when the
// session has nothing to say about it.
type measureDef struct {
	name   string
	unit   string
	higher bool
	value  func(SessionOutcome) (float64, bool)
}

var measureDefs = []measureDef{
	{"first-try instructions", "%", true, func(s SessionOutcome) (float64, bool) {
		return share(s.FirstTry, s.Instructions)
	}},
	{"reworked instructions", "%", false, func(s SessionOutcome) (float64, bool) {
		return share(s.Reworked, s.Instructions)
	}},
	{"repeated tool calls", "%", false, func(s SessionOutcome) (float64, bool) {
		return share(s.RepeatCalls, s.ToolCalls)
	}},
	{"tokens per instruction", "tokens", false, func(s SessionOutcome) (float64, bool) {
		return float64(s.TokensPerInstruction), s.Instructions > 0
	}},
	{"recorded outcomes achieved", "%", true, func(s SessionOutcome) (float64, bool) {
		v := outcomeScore(s.Outcome)
		return v * 100, v >= 0
	}},
}

func share(n, of int) (float64, bool) {
	if of <= 0 {
		return 0, false
	}
	return 100 * float64(n) / float64(of), true
}

// outcomeScore counts a partial as half, as intervention.Outcomes does;
// -1 is no recorded outcome.
func outcomeScore(o string) float64 {
	switch o {
	case "achieved":
		return 1
	case "partial":
		return 0.5
	case "not_achieved":
		return 0
	}
	return -1
}

func compare(d measureDef, fmtS, rawS []SessionOutcome, enough bool, measures int) Measure {
	fv, rv := values(d, fmtS), values(d, rawS)
	m := Measure{Name: d.name, Unit: d.unit, HigherIsBetter: d.higher, FmtSessions: len(fv), RawSessions: len(rv)}
	fm, fvar := meanVar(fv)
	rm, rvar := meanVar(rv)
	m.Fmt, m.Raw, m.Diff = round1(fm), round1(rm), round1(fm-rm)
	switch {
	case !enough:
		m.Reading = "not enough sessions yet"
		return m
	case len(fv) < MinExperimentSessions || len(rv) < MinExperimentSessions:
		m.Reading = fmt.Sprintf("too few sessions with a value: fmt %d, raw %d; each arm needs %d", len(fv), len(rv), MinExperimentSessions)
		return m
	}
	half := 1.96 * math.Sqrt(fvar/float64(len(fv))+rvar/float64(len(rv)))
	m.Low, m.High, m.Interval = round1(fm-rm-half), round1(fm-rm+half), true
	span := fmt.Sprintf("fmt %s against raw %s, difference %s (rough 95%% interval %s to %s)",
		figure(m.Fmt, d.unit), figure(m.Raw, d.unit), signed(m.Diff, d.unit), signed(m.Low, d.unit), signed(m.High, d.unit))
	if m.Low <= 0 && m.High >= 0 {
		m.Reading = "no difference shown: " + span
		return m
	}
	better := (m.Diff > 0) == d.higher
	word := "worse"
	if better {
		word = "better"
	}
	m.Reading = fmt.Sprintf("fmt sessions did %s: %s; one of %d measures, so read it as a lead to check, not a finding", word, span, measures)
	return m
}

func values(d measureDef, ss []SessionOutcome) []float64 {
	var out []float64
	for _, s := range ss {
		if v, ok := d.value(s); ok {
			out = append(out, v)
		}
	}
	return out
}

// meanVar is the mean and the sample variance.
func meanVar(vs []float64) (mean, variance float64) {
	if len(vs) == 0 {
		return 0, 0
	}
	for _, v := range vs {
		mean += v
	}
	mean /= float64(len(vs))
	if len(vs) < 2 {
		return mean, 0
	}
	for _, v := range vs {
		variance += (v - mean) * (v - mean)
	}
	return mean, variance / float64(len(vs)-1)
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func figure(f float64, unit string) string {
	if unit == "%" {
		return fmt.Sprintf("%.1f%%", f)
	}
	return fmt.Sprintf("%.0f %s", f, unit)
}

func signed(f float64, unit string) string {
	if unit == "%" {
		return fmt.Sprintf("%+.1f points", f)
	}
	return fmt.Sprintf("%+.0f %s", f, unit)
}

// ExperimentLines renders the report for a terminal.
func ExperimentLines(r ExperimentReport) []string {
	lines := []string{
		fmt.Sprintf("fmt arm: %d sessions, %d instructions, %d commands, ~%d tokens saved", r.Fmt.Sessions, r.Fmt.Instructions, r.Fmt.Runs, r.Fmt.TokensSaved),
		fmt.Sprintf("raw arm: %d sessions, %d instructions, %d commands, ~%d tokens fmt would have saved", r.Raw.Sessions, r.Raw.Instructions, r.Raw.Runs, r.Raw.TokensSaved),
	}
	if r.Mixed > 0 {
		lines = append(lines, fmt.Sprintf("%d session(s) ran in both arms (the share changed mid-session) and are left out", r.Mixed))
	}
	if !r.Enough {
		return append(lines, r.Why+".")
	}
	for _, m := range r.Measures {
		lines = append(lines, fmt.Sprintf("  %s: %s", m.Name, m.Reading))
	}
	return lines
}
