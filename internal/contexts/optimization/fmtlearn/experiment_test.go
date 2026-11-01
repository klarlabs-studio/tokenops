package fmtlearn

import (
	"fmt"
	"strings"
	"testing"
)

func TestExperimentArmIsFixedPerSession(t *testing.T) {
	for i := range 50 {
		s := fmt.Sprintf("session-%d", i)
		first := ExperimentArm(s, 50)
		if first != ArmFmt && first != ArmRaw {
			t.Fatalf("arm %q for %s", first, s)
		}
		for range 3 {
			if got := ExperimentArm(s, 50); got != first {
				t.Fatalf("%s drew %q then %q", s, first, got)
			}
		}
	}
}

func TestExperimentArmShare(t *testing.T) {
	count := func(percent int) int {
		raw := 0
		for i := range 2000 {
			if ExperimentArm(fmt.Sprintf("s%d", i), percent) == ArmRaw {
				raw++
			}
		}
		return raw
	}
	if n := count(50); n < 900 || n > 1100 {
		t.Errorf("50%%: %d of 2000 raw", n)
	}
	if n := count(10); n < 120 || n > 280 {
		t.Errorf("10%%: %d of 2000 raw", n)
	}
	// Above the maximum it is clamped, never more than half raw.
	if n, half := count(90), count(MaxExperimentRawPercent); n != half {
		t.Errorf("90%% drew %d raw, the clamped share %d", n, half)
	}
}

func TestExperimentArmOff(t *testing.T) {
	if got := ExperimentArm("s", 0); got != "" {
		t.Errorf("off drew %q", got)
	}
	if got := ExperimentArm("", 50); got != "" {
		t.Errorf("no session drew %q", got)
	}
}

// arm makes n sessions of an arm whose first-try share is firstTry of 10
// instructions, with a little spread so the interval has a width.
func arm(name string, n, firstTry int) []SessionOutcome {
	out := make([]SessionOutcome, 0, n)
	for i := range n {
		ft := firstTry + i%2
		out = append(out, SessionOutcome{
			Session: fmt.Sprintf("%s-%d", name, i), Arm: name, Runs: 3, TokensSaved: 100,
			Instructions: 10, FirstTry: ft, Reworked: 10 - ft,
			TokensPerInstruction: int64(5000 + 100*(i%3)), ToolCalls: 20, RepeatCalls: 2,
		})
	}
	return out
}

func measure(t *testing.T, r ExperimentReport, name string) Measure {
	t.Helper()
	for _, m := range r.Measures {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no measure %q", name)
	return Measure{}
}

func TestCompareArmsTooFewSessions(t *testing.T) {
	sessions := append(arm(ArmFmt, 3, 7), arm(ArmRaw, 6, 7)...)
	r := CompareArms(sessions, 50)
	if r.Enough {
		t.Fatal("3 fmt sessions passed the noise rule")
	}
	if r.Fmt.Sessions != 3 || r.Raw.Sessions != 6 || r.Fmt.Instructions != 30 {
		t.Errorf("counts: %+v %+v", r.Fmt, r.Raw)
	}
	if !strings.Contains(r.Why, "not enough sessions yet") {
		t.Errorf("why: %q", r.Why)
	}
	for _, m := range r.Measures {
		if m.Interval {
			t.Errorf("%s has an interval below the noise rule", m.Name)
		}
	}
	lines := strings.Join(ExperimentLines(r), "\n")
	if !strings.Contains(lines, "not enough sessions yet") || strings.Contains(lines, "interval") {
		t.Errorf("lines:\n%s", lines)
	}
}

func TestCompareArmsNoDifference(t *testing.T) {
	sessions := append(arm(ArmFmt, 8, 7), arm(ArmRaw, 8, 7)...)
	r := CompareArms(sessions, 50)
	if !r.Enough {
		t.Fatalf("8 sessions a side: %s", r.Why)
	}
	m := measure(t, r, "first-try instructions")
	if !m.Interval || m.Diff != 0 || m.Low > 0 || m.High < 0 {
		t.Errorf("first-try: %+v", m)
	}
	if !strings.HasPrefix(m.Reading, "no difference shown") {
		t.Errorf("reading: %q", m.Reading)
	}
}

func TestCompareArmsDifferenceIsALead(t *testing.T) {
	// fmt sessions get 3 of 10 instructions right first time, raw 8.
	sessions := append(arm(ArmFmt, 8, 3), arm(ArmRaw, 8, 8)...)
	r := CompareArms(sessions, 50)
	m := measure(t, r, "first-try instructions")
	if m.Diff >= 0 || m.High >= 0 {
		t.Fatalf("first-try: %+v", m)
	}
	if !strings.HasPrefix(m.Reading, "fmt sessions did worse") || !strings.Contains(m.Reading, "not a finding") {
		t.Errorf("reading: %q", m.Reading)
	}
	rw := measure(t, r, "reworked instructions")
	if !strings.HasPrefix(rw.Reading, "fmt sessions did worse") {
		t.Errorf("more rework read as %q", rw.Reading)
	}
}

func TestCompareArmsOutcomesNeedTheirOwnSessions(t *testing.T) {
	sessions := append(arm(ArmFmt, 8, 7), arm(ArmRaw, 8, 7)...)
	sessions[0].Outcome = "achieved"
	sessions[8].Outcome = "partial"
	r := CompareArms(sessions, 50)
	m := measure(t, r, "recorded outcomes achieved")
	if m.Interval || !strings.HasPrefix(m.Reading, "too few sessions with a value") {
		t.Errorf("outcomes on one session a side: %+v", m)
	}
	if r.Fmt.Assessed != 1 || r.Raw.Assessed != 1 || m.Fmt != 100 || m.Raw != 50 {
		t.Errorf("assessed: %+v %+v %+v", r.Fmt, r.Raw, m)
	}
}

func TestAnalyzeCountsTheRawArmAsProjected(t *testing.T) {
	recs := []Record{
		{Type: RecordCompress, Source: SourceWrapped, ID: "a", Command: "git", Handled: true, TokensSaved: 10},
		{Type: RecordCompress, Source: SourceWrapped, ID: "b", Command: "git", Handled: true, TokensSaved: 30, Arm: ArmRaw, Session: "s", RawPercent: 50},
	}
	r := Analyze(recs, Thresholds{})
	if r.ObservedRuns != 1 || r.ProjectedRuns != 1 || r.TokensSaved != 10 || r.ProjectedTokensSaved != 30 {
		t.Errorf("report: %+v", r)
	}
}
