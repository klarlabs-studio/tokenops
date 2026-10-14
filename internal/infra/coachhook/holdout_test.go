package coachhook

import (
	"fmt"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// sessionIn finds a session id the holdout assigns as want for kind.
func sessionIn(t *testing.T, kind string, want bool) string {
	t.Helper()
	for i := range 1000 {
		s := fmt.Sprint("session-", i)
		if learning.InHoldout(s, kind, learning.DefaultHoldoutPercent) == want {
			return s
		}
	}
	t.Fatal("no session found")
	return ""
}

func holdoutConfig() Config {
	cfg := compactConfig()
	cfg.HoldoutPercent = learning.DefaultHoldoutPercent
	return cfg
}

// A held session gets no tip: it is logged as shadow, with its expected
// saving and evidence, and does not count against the quiet policy.
func TestHoldoutHoldsAnEligibleTipAsShadow(t *testing.T) {
	dir := t.TempDir()
	session := sessionIn(t, compactKind, true)
	lines := turnsAt(nil, 0, CompactAfterTurns, 700_000)
	tp := writeTranscript(t, dir, lines...)
	d := Evaluate(dir, session, tp, holdoutConfig(), fixedNow)
	if d.Nudge || d.Ambient || d.OfferID != "" {
		t.Fatalf("held tip was delivered: %+v", d)
	}
	if !d.Holdout || d.ShadowID == "" || d.ShadowKind != compactKind || d.Suppressed != SuppressedHoldout {
		t.Fatalf("held tip not logged as shadow: %+v", d)
	}
	if d.ExpectedUSD <= 0 || d.ExpectedTokens <= 0 || d.Evidence == "" {
		t.Fatalf("shadow without expected saving or evidence: %+v", d)
	}
	st := loadSession(dir, session)
	if st.Nudges != 0 || st.LastNudgeAt != "" || !st.CompactTipped || st.ShadowTip == nil {
		t.Fatalf("state after a held tip = %+v", st)
	}
	// The held tip is settled like a shown one: a compaction follows it.
	lines = turnsAt(lines, CompactAfterTurns, 1, 80_000)
	rewrite(t, tp, lines...)
	d = Evaluate(dir, session, tp, holdoutConfig(), fixedNow)
	if len(d.ShadowResolved) != 1 || !d.ShadowResolved[0].Followed || d.ShadowResolved[0].Signal != learning.SignalCompacted {
		t.Fatalf("shadow resolution = %+v", d.ShadowResolved)
	}
	if len(d.Resolved) != 0 {
		t.Fatalf("a shadow tip resolved as a shown one: %+v", d.Resolved)
	}
}

// A session outside the holdout gets the tip, with its expected saving.
func TestHoldoutLeavesOtherSessionsAlone(t *testing.T) {
	dir := t.TempDir()
	session := sessionIn(t, compactKind, false)
	tp := writeTranscript(t, dir, turnsAt(nil, 0, CompactAfterTurns, 700_000)...)
	d := Evaluate(dir, session, tp, holdoutConfig(), fixedNow)
	if !d.Nudge || d.Holdout || d.OfferID == "" || d.ExpectedUSD <= 0 {
		t.Fatalf("tip outside the holdout = %+v", d)
	}
}

// Opted out, nothing is held.
func TestHoldoutOff(t *testing.T) {
	dir := t.TempDir()
	session := sessionIn(t, compactKind, true)
	cfg := holdoutConfig()
	cfg.HoldoutPercent = 0
	tp := writeTranscript(t, dir, turnsAt(nil, 0, CompactAfterTurns, 700_000)...)
	if d := Evaluate(dir, session, tp, cfg, fixedNow); !d.Nudge || d.Holdout {
		t.Fatalf("holdout off still held: %+v", d)
	}
}

// The spent budget always speaks, even in a held session.
func TestHoldoutNeverHoldsTheSpentBudget(t *testing.T) {
	dir := t.TempDir()
	session := sessionIn(t, "budget_50", true)
	cfg := holdoutConfig()
	cfg.CompactAtTokens = 0
	cfg.BudgetUSD = 0.40
	// One 1M cache-read turn on opus is $0.50: over the whole budget.
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000_000, opus))
	d := Evaluate(dir, session, tp, cfg, fixedNow)
	if !d.Nudge || d.Holdout || d.TipKind != "budget_over" {
		t.Fatalf("spent budget = %+v; want spoken", d)
	}
}

// While the coach only observes, the budget tier it would have spoken is
// recorded as a shadow tip, once, and nothing is said or latched.
func TestObservingRecordsShadowBudgetTips(t *testing.T) {
	dir := t.TempDir()
	cfg := tipConfig()
	cfg.Enabled = false
	cfg.BudgetUSD = 0.40
	// 500k of cache-read on opus is $0.25: 62% of the budget.
	tp := writeTranscript(t, dir, turnLine(ts(1), 500_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if d.Nudge || d.Holdout || d.ShadowID == "" || d.ShadowKind != "budget_50" || d.Evidence == "" {
		t.Fatalf("observed tier = %+v", d)
	}
	if st := loadSession(dir, "s"); st.MaxFiredFraction != 0 {
		t.Fatalf("observing latched the spoken tier: %+v", st)
	}
	rewrite(t, tp, turnLine(ts(1), 500_000, opus), turnLine(ts(2), 10_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.ShadowID != "" {
		t.Fatalf("the same tier recorded twice: %+v", d)
	}
}

// SessionPeaks is each session's highest spend in the ledger.
func TestSessionPeaks(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir, turnLine(ts(1), 2_000_000, opus))
	Evaluate(dir, "a", tp, DefaultConfig(), fixedNow)
	rewrite(t, tp, turnLine(ts(1), 2_000_000, opus), turnLine(ts(2), 2_000_000, opus))
	Evaluate(dir, "a", tp, DefaultConfig(), fixedNow)
	peaks := SessionPeaks(dir)
	if got := peaks["a"]; got < 1.99 || got > 2.01 {
		t.Fatalf("peak = %v; want $2", got)
	}
}
