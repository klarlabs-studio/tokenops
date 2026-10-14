package learning

import (
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/intervention"
)

var s0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// Safety tips always speak: only the tips about cost and drift may be
// held back, and nothing at or past a boundary.
func TestHoldoutEligibleExcludesSafety(t *testing.T) {
	for kind, want := range map[string]bool{
		"budget_50": true, "budget_75": true, "compact_now": true,
		"budget_over": false, "quota_100": false, "quota_90": false, "quota_burn": false,
		"quota_50": false, "reading_lost": false, "": false, "lookup": false,
	} {
		if got := HoldoutEligible(kind); got != want {
			t.Errorf("HoldoutEligible(%q) = %v; want %v", kind, got, want)
		}
	}
}

// Assignment is per session and family, so a held session stays held for
// the whole episode, and the share held stays near the percent.
func TestInHoldoutIsStableAndSmall(t *testing.T) {
	if InHoldout("s", "budget_50", 0) || InHoldout("", "budget_50", 10) {
		t.Fatal("holdout off or no session must hold nothing")
	}
	held := 0
	const n = 4000
	for i := range n {
		s := fmt.Sprintf("session-%d", i)
		a := InHoldout(s, "budget_50", 10)
		if a != InHoldout(s, "budget_75", 10) {
			t.Fatalf("%s: budget_50 and budget_75 assigned differently", s)
		}
		if a {
			held++
		}
	}
	if share := float64(held) / n; share < 0.08 || share > 0.12 {
		t.Fatalf("held share = %.3f; want ~0.10", share)
	}
	// A percent above the ceiling is clamped, never more than 10%.
	held = 0
	for i := range n {
		if InHoldout(fmt.Sprintf("session-%d", i), "compact_now", 90) {
			held++
		}
	}
	if share := float64(held) / n; share > 0.12 {
		t.Fatalf("clamped share = %.3f; want <= ~0.10", share)
	}
}

func tip(id, session string, c Cohort, usdAtTip float64) SavingsEntry {
	return SavingsEntry{Type: SavingsIntervention, ID: id, At: s0, Session: session,
		Capability: CapabilityCompaction, Kind: "compact_now", Cohort: c,
		ExpectedUSD: 0.30, ExpectedBasis: "per turn", SessionUSD: usdAtTip}
}

// Realized saving comes only from the randomized holdout: treated
// sessions spend less after the tip than held ones, and the saving is
// that difference, not the expected figure.
func TestSummarizeRealizedFromHoldout(t *testing.T) {
	entries := make([]SavingsEntry, 0, 12)
	peaks := map[string]float64{}
	for i := range 6 {
		tr, ho := fmt.Sprintf("t%d", i), fmt.Sprintf("h%d", i)
		entries = append(entries, tip(tr, tr, CohortTreated, 10), tip(ho, ho, CohortHoldout, 10))
		peaks[tr], peaks[ho] = 14, 20 // $4 after the tip vs $10
	}
	got := SummarizeSavings(entries, Implicit{SessionPeakUSD: peaks}, s0.Add(48*time.Hour))
	if len(got) != 1 {
		t.Fatalf("summaries = %+v", got)
	}
	k := got[0]
	if k.Treated.Count != 6 || k.Holdout.Count != 6 {
		t.Fatalf("cohorts = %d/%d", k.Treated.Count, k.Holdout.Count)
	}
	if k.Comparison.Assignment != intervention.Randomised || k.Verdict.Outcome != intervention.Improved {
		t.Fatalf("verdict = %+v (%+v)", k.Verdict, k.Comparison)
	}
	per, ok := k.RealizedPerTipUSD.Amount()
	if !ok || per < 5.99 || per > 6.01 {
		t.Fatalf("realized per tip = %v", k.RealizedPerTipUSD)
	}
	total, ok := k.RealizedUSD.Amount()
	if !ok || total < 35.9 || total > 36.1 {
		t.Fatalf("realized total = %v", k.RealizedUSD)
	}
	if k.ExpectedPerTipUSD < 0.29 || k.ExpectedPerTipUSD > 0.31 {
		t.Fatalf("expected = %v", k.ExpectedPerTipUSD)
	}
}

// Too few held sessions is not a saving of zero: it is unknown, and says
// how many more are needed.
func TestSummarizeTooFewIsUnknown(t *testing.T) {
	entries := []SavingsEntry{tip("a", "a", CohortTreated, 1), tip("b", "b", CohortHoldout, 1)}
	k := SummarizeSavings(entries, Implicit{SessionPeakUSD: map[string]float64{"a": 2, "b": 3}}, s0.Add(48*time.Hour))[0]
	if k.RealizedUSD.Known() || k.Verdict.Conclusive() {
		t.Fatalf("realized = %v, verdict = %+v; want unknown", k.RealizedUSD, k.Verdict)
	}
	if k.RealizedUSD.Caveat() == "" {
		t.Fatal("an unknown realized saving must say why")
	}
}

// Implicit outcomes: a shadow tip resolved by a compaction counts as
// followed in its cohort, and a commit that landed is the outcome the
// comparison guards.
func TestSummarizeImplicitOutcomes(t *testing.T) {
	entries := []SavingsEntry{
		tip("a", "sa", CohortTreated, 1),
		tip("b", "sb", CohortHoldout, 1),
		{Type: SavingsOutcome, ID: "b", At: s0.Add(time.Minute), Signal: SignalCompacted, Followed: true},
	}
	implicit := Implicit{
		Followed: map[string]bool{"a": false},
		CommitLanded: func(session string, _ time.Time) (bool, bool) {
			return session == "sa", session != ""
		},
	}
	k := SummarizeSavings(entries, implicit, s0.Add(48*time.Hour))[0]
	if k.Holdout.Followed != 1 || k.Holdout.Resolved != 1 || k.Treated.Followed != 0 || k.Treated.Resolved != 1 {
		t.Fatalf("follow-through = treated %+v holdout %+v", k.Treated, k.Holdout)
	}
	if k.Comparison.InterventionOutcomes.Achieved != 1 || k.Comparison.BaselineOutcomes.NotAchieved != 1 {
		t.Fatalf("commit outcomes = %+v / %+v", k.Comparison.InterventionOutcomes, k.Comparison.BaselineOutcomes)
	}
}

// Rows for interventions that are not tips (a refused re-read) carry
// their expected saving and count, with no cohort to compare.
func TestSummarizeActionsWithoutHoldout(t *testing.T) {
	entries := []SavingsEntry{
		{Type: SavingsIntervention, ID: "r1", At: s0, Session: "s", Capability: CapabilityReadGuard, Kind: "reread", Cohort: CohortTreated, ExpectedTokens: 4000},
		{Type: SavingsIntervention, ID: "r2", At: s0, Session: "s", Capability: CapabilityReadGuard, Kind: "reread", Cohort: CohortShadow, ExpectedTokens: 1000},
	}
	k := SummarizeSavings(entries, Implicit{}, s0)[0]
	if k.Treated.Count != 1 || k.Shadow.Count != 1 || k.ShadowTokens != 1000 || k.ExpectedTokens != 4000 || k.Holdout.Count != 0 {
		t.Fatalf("read guard = %+v", k)
	}
}

func TestCapabilityOf(t *testing.T) {
	for kind, want := range map[string]string{
		"compact_now": CapabilityCompaction, "budget_50": CapabilitySessionBudget, "budget_over": CapabilitySessionBudget,
		"quota_100": CapabilityPlanWindow, "quota_burn": CapabilityPlanWindow,
	} {
		if got := CapabilityOf(kind); got != want {
			t.Errorf("CapabilityOf(%q) = %q; want %q", kind, got, want)
		}
	}
}
