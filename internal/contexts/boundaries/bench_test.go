package boundaries

import (
	"fmt"
	"testing"
)

// BenchmarkEvaluate is the in-memory half of the pause point's budget
// (ADR 0014: under 5 ms per evaluation, no store reads). Twenty rules is
// more than any config is expected to carry.
func BenchmarkEvaluate(b *testing.B) {
	specs := make([]RuleSpec, 0, 20)
	for i := range 20 {
		specs = append(specs, RuleSpec{
			Name: fmt.Sprintf("r%d", i), Action: "continue", Require: "ask", Stage: "enforce",
			When: map[string]string{"session_cost_usd": fmt.Sprintf(">= %d", i), "plan_left_pct": "< 50", "window": "week"},
		})
	}
	rules, err := Parse(specs)
	if err != nil {
		b.Fatal(err)
	}
	f := Facts{SessionCostUSD: Known(12), PlanLeftPct: map[Window]float64{WindowWeek: 30, Window5h: 70}, WorkflowID: "claude-code:p:s"}
	b.ReportAllocs()
	for b.Loop() {
		_ = Evaluate(rules, ActionContinue, f)
	}
}
