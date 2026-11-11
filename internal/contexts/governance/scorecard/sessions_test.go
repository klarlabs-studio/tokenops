package scorecard

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// Session quality is the scorecard's: the DX grade of the operator's own
// instructions counts toward the one grade and can set it (ADR 0018).
func TestSessionQualityIsGraded(t *testing.T) {
	sc := NewWithAgentKPIs(math.NaN(), math.NaN(), 100, AgentKPIInputs{
		CacheHitRatioPct: 95, CacheHitRatioComputed: true,
		Sessions: &SessionGrade{Letter: GradeC, Driver: "rework", Instructions: 412},
	}, "")
	if sc.OverallGrade != GradeC || sc.OverallDriver != "session quality" {
		t.Fatalf("overall %q from %q, want C from session quality", sc.OverallGrade, sc.OverallDriver)
	}
	out := sc.String()
	for _, want := range []string{"Grade: C  (the worst graded measure: session quality)", "[C] over 412 of your instructions"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// It survives the daemon's wire.
	b, err := sc.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var back Scorecard
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Sessions.Grade != GradeC || back.OverallDriver != "session quality" || back.String() != out {
		t.Errorf("read back: sessions %+v, driver %q", back.Sessions, back.OverallDriver)
	}
}

// A period the session analysis does not cover says why it has no
// session grade, and the rest of the scorecard still grades.
func TestMissingSessionQualitySaysWhy(t *testing.T) {
	sc := Build(context.Background(), nil, BuildParams{SinceDays: 30, AgentKPIs: AgentKPIInputs{
		ToolSuccessRatePct: 97, ToolSuccessComputed: true,
		Sessions: &SessionGrade{Missing: "it covers the last 7 days; `tokenops dx --days 30` grades these"},
	}})
	if sc.Sessions.Grade != "" || sc.OverallGrade != GradeA {
		t.Fatalf("sessions %+v, overall %q", sc.Sessions, sc.OverallGrade)
	}
	if !strings.Contains(sc.String(), "not graded: it covers the last 7 days") {
		t.Errorf("no reason given:\n%s", sc.String())
	}
	b, _ := sc.MarshalJSON()
	if !strings.Contains(string(b), `"note": "it covers the last 7 days`) {
		t.Errorf("the reason is not in the JSON:\n%s", b)
	}
	// A session grade alone is enough to grade.
	only := Build(context.Background(), nil, BuildParams{AgentKPIs: AgentKPIInputs{Sessions: &SessionGrade{Letter: GradeB, Instructions: 50}}})
	if only.OverallGrade != GradeB {
		t.Errorf("a session grade alone: %q", only.OverallGrade)
	}
}
