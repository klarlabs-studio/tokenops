package scorecard

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// overWire is s as a client that asked the daemon gets it.
func overWire(t *testing.T, s *Scorecard) *Scorecard {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got Scorecard
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return &got
}

// The scorecard read back from the daemon's JSON reads as the one
// computed here, to the character, and says the same thing again on the
// wire: the CLI prints what the daemon computed.
func TestScorecardReadsTheSameFromTheWire(t *testing.T) {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	util, cov, change, base := 62.0, 90.0, -12.5, 4.0

	full := NewWithAgentKPIs(45, 31.2, 88.1, AgentKPIInputs{
		CacheHitRatioPct: 71, CacheHitRatioComputed: true,
		ConfirmationGateRatePct: 25, ConfirmationGateComputed: true,
		RegenerateRatePct: 3.5, RegenerateComputed: true,
		ToolSuccessRatePct: 96.25, ToolSuccessComputed: true,
		DestructiveRatePct: 0.125, DestructiveComputed: true,
	}, "v1.2")
	full.GeneratedAt = at
	full.FirstValueTime.VsBaseline = &Comparison{Baseline: 60, Delta: -15, Direction: "better"}
	full.SelfBaseline = &SelfBaseline{Since: at.AddDate(0, 0, -35), Until: at.AddDate(0, 0, -7), Days: 28}
	full.PlanUse = &plans.PlanUseReport{Providers: []plans.PlanUse{{Provider: "anthropic", UtilizationPct: &util,
		UtilizationWindow: "weekly", CoveragePct: &cov, Windows: []plans.WindowUse{{Label: "weekly", Windows: 1, AvgPeakPct: util}}}}}
	full.PlanUseDays = 14
	full.PlanUseVsBaseline = []PlanUseComparison{{Provider: "anthropic", Utilization: &Comparison{Baseline: 50, Delta: 12, Direction: "unchanged"}}}
	full.UnitCosts = []UnitCost{{Unit: "commit", Units: 12, PerUnitUSD: 3.5, BaselinePerUnitUSD: &base, ChangePct: &change, Direction: "better"}}
	full.Allocation = &AllocationCoverage{Rules: 2, TaggedPct: 80, ByKeyPct: map[string]float64{"project": 80}}

	// FVT, TEU and SAC not measured; the store could not be read whole.
	partial := New(math.NaN(), math.NaN(), 72, "")
	partial.GeneratedAt = at
	partial.Degraded = "the events could not be read in time"
	partial.PlanUseDays = 7

	warm := NewWarmingUp("first week")
	warm.GeneratedAt = at
	warm.Checklist[0].Completed = true
	// Nothing measured because the store could not be read.
	unread := NewWarmingUp("")
	unread.GeneratedAt = at
	unread.Degraded = "the events could not be read in time"

	for name, s := range map[string]*Scorecard{"full": full, "partly measured": partial, "warming up": warm, "unread": unread} {
		got := overWire(t, s)
		if got.String() != s.String() {
			t.Errorf("%s: read back from the wire\n%s\nwant\n%s", name, got.String(), s.String())
		}
		a, _ := json.Marshal(s)
		b, _ := json.Marshal(got)
		if string(a) != string(b) {
			t.Errorf("%s: on the wire again\n%s\nwant\n%s", name, b, a)
		}
	}
}

// A scorecard warming up because the store could not be read says so:
// otherwise it reads as a week where nothing happened.
func TestWarmingUpSaysWhenTheStoreWasNotRead(t *testing.T) {
	s := NewWarmingUp("")
	s.Degraded = "the events could not be read in time"
	if out := s.String(); !strings.Contains(out, "could not be computed: the events could not be read in time") {
		t.Errorf("warming up hides why:\n%s", out)
	}
}

// Why KPIs are missing reaches a client: an unreadable store and an
// unmeasured metric read the same otherwise.
func TestScorecardWireSaysWhyItIsDegraded(t *testing.T) {
	s := New(math.NaN(), math.NaN(), math.NaN(), "")
	s.Degraded = "the events could not be read in time"
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Degraded string `json:"degraded"`
	}
	if err := json.Unmarshal(b, &w); err != nil || w.Degraded != s.Degraded {
		t.Errorf("degraded on the wire = %q, %v; want %q", w.Degraded, err, s.Degraded)
	}
}
