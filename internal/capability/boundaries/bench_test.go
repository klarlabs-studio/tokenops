package boundaries

import (
	"slices"
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

// The pause point runs before every tool call, so ADR 0014 gives it a
// budget: under 5 ms per evaluation, with no event-store reads. These
// measure the whole evaluation — the figure files read, the rules
// matched, the per-session counters written — on the paths a session
// takes: nothing crossed, a rule crossed on every call, and a crossed
// rule a presence-signed answer approves.
//
// Each setup prepares a session and returns one evaluation, which fails
// tb when the evaluation errs or takes an unexpected turn; the benchmarks
// and the budget gate run the same evaluations.
type guardEval func(tb testing.TB) func()

func spendGuard(cost float64) guardEval {
	return func(tb testing.TB) func() {
		env := testEnv(tb, false)
		env.Now = time.Now
		setCost(tb, env, "s1", cost)
		if err := claudelimits.Write(env.LimitsPath, claudelimits.Reading{
			ObservedAt: time.Now(), SevenDay: &claudelimits.Window{UsedPct: 40, ResetsAt: time.Now().Add(48 * time.Hour).Unix()},
		}); err != nil {
			tb.Fatal(err)
		}
		r, err := domain.Parse([]domain.RuleSpec{
			{Name: "session-spend", Action: "continue", Require: "ask", When: map[string]string{"session_cost_usd": ">= 20"}},
			{Name: "plan-floor", Action: "continue", Require: "deny", When: map[string]string{"plan_left_pct": "< 5", "window": "week"}},
		})
		if err != nil {
			tb.Fatal(err)
		}
		c := call("s1")
		return func() {
			if _, err := Guard(r, c, env); err != nil {
				tb.Fatal(err)
			}
		}
	}
}

// signedAnswer is the path ADR 0015 adds: a rule crossed and a
// presence-signed session approval waiting, so every call reads the
// answer, the key store and the pending decision, and verifies the
// signature (validation step 5). It is measured with presence enforced
// (boundary_approval: presence), where the check decides whether the
// answer counts.
func signedAnswer(tb testing.TB) func() {
	env := testEnv(tb, false)
	env.Now = time.Now
	setCost(tb, env, "s1", 25)
	priv := enrol(tb, env)
	r, err := domain.Parse([]domain.RuleSpec{
		{Name: "session-spend", Action: "continue", Require: "ask", Stage: "enforce", When: map[string]string{"session_cost_usd": ">= 20"}},
	})
	if err != nil {
		tb.Fatal(err)
	}
	c := call("s1")
	first, err := Guard(r, c, env)
	if err != nil || first.DecisionID == "" {
		tb.Fatalf("%+v %v", first, err)
	}
	if _, err := Decide(env, DecideRequest{ID: first.DecisionID, Decision: "approve", Session: true}); err != nil {
		tb.Fatal(err)
	}
	signAnswer(tb, env, priv, first.DecisionID)
	env.Approval = presence.ModePresence
	if _, err := Guard(r, c, env); err != nil {
		tb.Fatal(err)
	}
	if a, _ := appliedApproval(tb, env, first.DecisionID); a.Presence != PresenceSigned {
		tb.Fatalf("the signed answer did not verify: %+v", a)
	}
	return func() {
		res, err := Guard(r, c, env)
		if err != nil || res.Verdict != VerdictNone {
			tb.Fatalf("%+v %v", res, err)
		}
	}
}

func benchGuard(b *testing.B, setup guardEval) {
	eval := setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		eval()
	}
}

func BenchmarkGuardNothingCrossed(b *testing.B) { benchGuard(b, spendGuard(5)) }

func BenchmarkGuardRuleCrossed(b *testing.B) { benchGuard(b, spendGuard(25)) }

func BenchmarkGuardSignedAnswer(b *testing.B) { benchGuard(b, signedAnswer) }

// TestGuardIsWithinItsBudget is the gate: a regression past 5 ms per
// evaluation fails the suite, not just a benchmark nobody reads. A
// crossed rule costs one append to the session's match log, about 0.3 ms
// with the machine loaded; it rewrote the session's file before, 1.4 ms
// and more than 5 ms when the disk was busy (#686). The signed-answer
// path (ADR 0015 validation step 5) is held to the same budget: the key
// store's two small files, the pending decision and one ECDSA P-256
// verification, about 0.1 ms of it the signature.
func TestGuardIsWithinItsBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing gate")
	}
	for name, setup := range map[string]guardEval{
		"rule crossed":  spendGuard(25),
		"signed answer": signedAnswer,
	} {
		t.Run(name, func(t *testing.T) { withinBudget(t, setup) })
	}
}

// The gate times evaluations one at a time and judges the median call,
// keeping the best of a few rounds. A benchmark's mean is the wrong
// figure on a shared machine: a call the scheduler preempts waits out
// other processes' time slices, and at a load average of 45 to 100 on ten
// cores those few long waits pulled the mean of 0.3 ms calls past 5 ms.
// The median ignores that tail as long as most calls run uninterrupted,
// while a regression in the evaluation itself slows every call, so it
// moves the median of every round and still fails the gate.
const (
	gateRounds = 5
	// gateCalls is odd so the median is one measured call.
	gateCalls = 101
)

func withinBudget(t *testing.T, setup guardEval) {
	eval := setup(t)
	best := time.Duration(-1)
	for range gateRounds {
		median := medianCall(eval, gateCalls)
		if best < 0 || median < best {
			best = median
		}
		if best <= guardBudget {
			return
		}
	}
	t.Fatalf("boundary evaluation takes %s per call (median of %d calls, best of %d rounds); the budget is %s",
		best, gateCalls, gateRounds, guardBudget)
}

// medianCall runs eval n times and returns the median wall time of one call.
func medianCall(eval func(), n int) time.Duration {
	took := make([]time.Duration, n)
	for i := range took {
		start := time.Now()
		eval()
		took[i] = time.Since(start)
	}
	slices.Sort(took)
	return took[n/2]
}

// guardBudget is ADR 0014's limit for one evaluation at the pause point.
const guardBudget = 5 * time.Millisecond
