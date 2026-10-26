package config

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// The default loop breaker is a continue rule on loop_calls >= 4, asking,
// in shadow.
func TestLoopBreakerRuleIsAShadowContinueAtFourCalls(t *testing.T) {
	spec := LoopBreakerRule()
	if spec.Name != LoopBreakerName || spec.Stage != string(boundaries.StageShadow) {
		t.Fatalf("rule = %+v", spec)
	}
	rules, err := boundaries.Parse([]boundaries.RuleSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	r := rules[0]
	if r.Action != boundaries.ActionContinue || r.Require.Kind != boundaries.RequireAsk || r.Enforced() {
		t.Fatalf("parsed = %+v", r)
	}
	if len(r.When) != 1 || r.When[0].Field != boundaries.FieldLoopCalls || r.When[0].Op != boundaries.OpGE || r.When[0].Number != 4 {
		t.Fatalf("condition = %+v, want loop_calls >= 4", r.When)
	}
	if !(Config{Boundaries: []boundaries.RuleSpec{spec}}).HasLoopBreaker() || (Config{}).HasLoopBreaker() {
		t.Fatal("HasLoopBreaker does not see the rule")
	}
}
