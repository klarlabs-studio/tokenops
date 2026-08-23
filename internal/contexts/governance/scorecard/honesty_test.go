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
	if !strings.Contains(out, "TEU — Token Efficiency Uplift (%):          N/A") {
		t.Errorf("TEU should render N/A, got:\n%s", out)
	}
	if strings.Contains(out, "15.0 [B]") {
		t.Errorf("the 15%% default leaked into output:\n%s", out)
	}
}

// An uncomputed metric must not contribute a grade to the overall
// average — otherwise a placeholder silently props the summary up.
func TestUncomputedMetricsExcludedFromOverall(t *testing.T) {
	all := NewWithAgentKPIs(12, 40, 95, AgentKPIInputs{}, "")
	partial := NewWithAgentKPIs(12, math.NaN(), 95, AgentKPIInputs{}, "")
	if all.OverallGrade == "" || partial.OverallGrade == "" {
		t.Fatalf("overall grades must be set: %q / %q", all.OverallGrade, partial.OverallGrade)
	}
	// FVT 12s = A, SAC 95% = A. With TEU excluded the overall is pure A;
	// with a graded TEU of 40% (also A) it is likewise A. The point is
	// that excluding a metric never *lowers* the result below the graded
	// ones — a phantom F or C must not appear.
	if partial.OverallGrade != GradeA {
		t.Errorf("overall = %q, want A from the two computed A metrics", partial.OverallGrade)
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
	if !strings.Contains(sc.String(), "FVT — First-Value Time (seconds):           N/A") {
		t.Errorf("FVT should render N/A, got:\n%s", sc.String())
	}
}

// Computed metrics keep grading exactly as before.
func TestComputedMetricsUnchanged(t *testing.T) {
	sc := NewWithAgentKPIs(12, 40, 95, AgentKPIInputs{}, "")
	out := sc.String()
	for _, want := range []string{
		"FVT — First-Value Time (seconds):           12.0 [A]",
		"TEU — Token Efficiency Uplift (%):          40.0 [A]",
		"SAC — Spend Attribution Completeness (%):   95.0 [A]",
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
