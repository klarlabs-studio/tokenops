package scorecard

import (
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// TEU has no meaning until the optimizer has actually run. Substituting
// a default and grading it B presents an invention as a measurement —
// the opposite of the "honest signal quality" the product promises.
func TestUncomputedTEURendersNotApplicable(t *testing.T) {
	sc := NewWithAgentKPIs(12, math.NaN(), 90, AgentKPIInputs{}, "")
	if sc.TokenEfficiency.Grade != "" {
		t.Errorf("TEU graded %q, want no grade when uncomputed", sc.TokenEfficiency.Grade)
	}
	out := sc.String()
	if strings.Contains(out, "tokens saved") {
		t.Errorf("an unmeasured TEU is listed:\n%s", out)
	}
	if strings.Contains(out, "15.0 [B]") {
		t.Errorf("the 15%% default leaked into output:\n%s", out)
	}
}

// An uncomputed metric must not contribute a grade to the overall —
// otherwise a placeholder silently props the summary up — and neither do
// the reported ones (FVT, SAC; ADR 0018). With nothing graded measured
// there is no grade, rather than an invented F.
func TestUncomputedAndReportedMetricsExcludedFromOverall(t *testing.T) {
	all := NewWithAgentKPIs(12, 40, 95, AgentKPIInputs{}, "")
	if all.OverallGrade != GradeA || all.OverallDriver != "tokens saved" {
		t.Errorf("overall = %q from %q, want A from TEU alone", all.OverallGrade, all.OverallDriver)
	}
	partial := NewWithAgentKPIs(500, math.NaN(), 10, AgentKPIInputs{}, "")
	if partial.OverallGrade != "" {
		t.Errorf("overall = %q from a slow FVT and a low SAC, which are reported, not graded", partial.OverallGrade)
	}
	if !strings.Contains(partial.String(), "Grade: none yet") {
		t.Errorf("no grade is not said:\n%s", partial.String())
	}
}

// FVT is derived from PromptEvent.Latency, which the passive JSONL
// readers never populate. A median of an always-zero field is zero, and
// zero seconds grades A — a perfect score for absent data.
func TestUncomputedFVTRendersNotApplicable(t *testing.T) {
	sc := NewWithAgentKPIs(math.NaN(), 30, 90, AgentKPIInputs{}, "")
	if sc.FirstValueTime.Grade != "" {
		t.Errorf("FVT graded %q, want no grade when uncomputed", sc.FirstValueTime.Grade)
	}
	if !strings.Contains(sc.String(), "first prompt after a session starts (s):   not measured") {
		t.Errorf("FVT should render N/A, got:\n%s", sc.String())
	}
}

// Computed metrics render: the graded with their letter, the reported
// without one.
func TestComputedMetricsRender(t *testing.T) {
	sc := NewWithAgentKPIs(12, 40, 95, AgentKPIInputs{}, "")
	out := sc.String()
	for _, want := range []string{
		"tokens saved (% of input):                 40.0 [A]",
		"first prompt after a session starts (s):   12.0\n",
		"prompts with a workflow or session (%):    95.0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// An unmeasured KPI must still serialise. encoding/json refuses NaN, so
// the N/A path has to drop the block rather than emit an invalid float.
func TestUncomputedKPIsStillMarshal(t *testing.T) {
	sc := NewWithAgentKPIs(math.NaN(), math.NaN(), 95, AgentKPIInputs{}, "")
	b, err := sc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if strings.Contains(got, "NaN") {
		t.Errorf("NaN leaked into JSON:\n%s", got)
	}
	if strings.Contains(got, "first_value_time") {
		t.Errorf("unmeasured FVT should be omitted, got:\n%s", got)
	}
	if !strings.Contains(got, "spend_attribution_completeness") {
		t.Errorf("measured SAC should be present, got:\n%s", got)
	}
}

// The passive JSONL readers never populate PromptEvent.Latency, so every
// value is the zero Duration. Taking a median over that yields 0s, which
// grades A — a perfect First-Value Time awarded for absent data. FVT
// must only count sessions that carry a real latency.
func TestFVTIgnoresUnpopulatedLatency(t *testing.T) {
	var out LiveKPIs
	computeFVT(&out, []*eventschema.Envelope{
		{Payload: &eventschema.PromptEvent{SessionID: "a"}},
		{Payload: &eventschema.PromptEvent{SessionID: "b"}},
	})
	if out.FVTComputed {
		t.Errorf("FVT reported as computed (%.1fs) from events with no latency", out.FVTSeconds)
	}
}

// With real latencies present, FVT computes as before.
func TestFVTUsesRealLatencies(t *testing.T) {
	var out LiveKPIs
	computeFVT(&out, []*eventschema.Envelope{
		{Payload: &eventschema.PromptEvent{SessionID: "a", Latency: 10 * time.Second}},
		{Payload: &eventschema.PromptEvent{SessionID: "b", Latency: 30 * time.Second}},
		{Payload: &eventschema.PromptEvent{SessionID: "c"}},
	})
	if !out.FVTComputed {
		t.Fatalf("FVT should compute when latencies exist")
	}
	if out.FVTSeconds != 30 {
		t.Errorf("FVTSeconds = %v, want 30 (median of the two real values)", out.FVTSeconds)
	}
}
