package boundaries

import (
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

// The pause point runs before every tool call, so ADR 0014 gives it a
// budget: under 5 ms per evaluation, with no event-store reads. These
// measure the whole evaluation — the figure files read, the rules
// matched, the per-session counters written — on the paths a session
// takes: nothing crossed, and a rule crossed on every call.
func benchGuard(b *testing.B, cost float64) {
	env := testEnv(b, false)
	env.Now = time.Now
	setCost(b, env, "s1", cost)
	if err := claudelimits.Write(env.LimitsPath, claudelimits.Reading{
		ObservedAt: time.Now(), SevenDay: &claudelimits.Window{UsedPct: 40, ResetsAt: time.Now().Add(48 * time.Hour).Unix()},
	}); err != nil {
		b.Fatal(err)
	}
	r, err := domain.Parse([]domain.RuleSpec{
		{Name: "session-spend", Action: "continue", Require: "ask", When: map[string]string{"session_cost_usd": ">= 20"}},
		{Name: "plan-floor", Action: "continue", Require: "deny", When: map[string]string{"plan_left_pct": "< 5", "window": "week"}},
	})
	if err != nil {
		b.Fatal(err)
	}
	c := call("s1")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Guard(r, c, env); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGuardNothingCrossed(b *testing.B) { benchGuard(b, 5) }

func BenchmarkGuardRuleCrossed(b *testing.B) { benchGuard(b, 25) }

// TestGuardIsWithinItsBudget is the gate: a regression past 5 ms per
// evaluation fails the suite, not just a benchmark nobody reads.
func TestGuardIsWithinItsBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing gate")
	}
	res := testing.Benchmark(BenchmarkGuardRuleCrossed)
	if per := time.Duration(res.NsPerOp()); per > 5*time.Millisecond {
		t.Fatalf("boundary evaluation takes %s per call; the budget is 5ms", per)
	}
}
