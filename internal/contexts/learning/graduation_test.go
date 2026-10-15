package learning

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

func TestStageOf(t *testing.T) {
	for rung, want := range map[string]Stage{"off": StageShadow, "advise": StageAdvise, "ask": StageAct, "autonomous": StageAct, "": StageShadow} {
		if got := StageOf(rung); got != want {
			t.Errorf("StageOf(%q) = %q; want %q", rung, got, want)
		}
	}
}

// The read guard is ready to enforce once its shadow calls are many and
// precise and the re-reads it watched go by cost something.
func TestReadGuardReadyToEnforce(t *testing.T) {
	ev := GraduationEvidence{Shadow: 60, ShadowConfirmed: 60,
		Realized: measurement.Measured(240_000, "read_guard_shadow"), RealizedUnit: "tokens"}
	g := Graduate(CapabilityReadGuard, StageAdvise, ev)
	if !g.Ready || g.Next != StageAct {
		t.Fatalf("graduation = %+v", g)
	}
	for _, c := range g.Criteria {
		if !c.Met || c.Want == "" || c.Have == "" {
			t.Errorf("criterion %+v", c)
		}
	}
	// Short of 50 shadow calls it is not ready, and says how far.
	ev.Shadow, ev.ShadowConfirmed = 30, 30
	g = Graduate(CapabilityReadGuard, StageAdvise, ev)
	if g.Ready || !strings.Contains(g.Criteria[0].Have, "30") {
		t.Fatalf("graduation at 30 = %+v", g)
	}
}

// A tip graduates to acting only on advice that was acted on and a
// saving the holdout realized; harm blocks it whatever else holds.
func TestTipsNeedARealizedSaving(t *testing.T) {
	ev := GraduationEvidence{Advised: 80, Accepted: 76, Realized: measurement.Unknown("not established")}
	g := Graduate(CapabilityCompaction, StageAdvise, ev)
	if g.Ready {
		t.Fatalf("ready without a realized saving: %+v", g)
	}
	ev.Realized = measurement.Measured(31.2, "randomized holdout")
	ev.RealizedUnit = "usd"
	if g = Graduate(CapabilityCompaction, StageAdvise, ev); !g.Ready {
		t.Fatalf("not ready: %+v", g)
	}
	ev.Harmed = true
	if g = Graduate(CapabilityCompaction, StageAdvise, ev); g.Ready {
		t.Fatalf("ready despite harm: %+v", g)
	}
	ev.Harmed, ev.Accepted = false, 60 // 75%
	if g = Graduate(CapabilityCompaction, StageAdvise, ev); g.Ready {
		t.Fatalf("ready at 75%% precision: %+v", g)
	}
}

// From shadow, a capability graduates to advising on precise shadow calls.
func TestShadowToAdvise(t *testing.T) {
	g := Graduate(CapabilitySessionBudget, StageShadow, GraduationEvidence{Shadow: 50, ShadowConfirmed: 46})
	if !g.Ready || g.Next != StageAdvise {
		t.Fatalf("graduation = %+v", g)
	}
	if g = Graduate(CapabilitySessionBudget, StageShadow, GraduationEvidence{Shadow: 50, ShadowConfirmed: 40}); g.Ready {
		t.Fatalf("ready at 80%%: %+v", g)
	}
}

// Acting is the top of the ladder: nothing further to earn.
func TestActingHasNoNextStage(t *testing.T) {
	g := Graduate(CapabilityRouting, StageAct, GraduationEvidence{})
	if g.Ready || g.Next != "" || len(g.Criteria) != 0 {
		t.Fatalf("graduation = %+v", g)
	}
}
