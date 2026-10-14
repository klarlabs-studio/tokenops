package coachhook

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// seedHistory appends one ledger event per figure: a past session that
// peaked at that cost.
func seedHistory(t *testing.T, dir string, at time.Time, usd ...float64) {
	t.Helper()
	for i, v := range usd {
		appendLedger(dir, ledgerEvent{TS: at, Session: fmt.Sprintf("past-%d", i), CumulativeUSD: v / 2})
		appendLedger(dir, ledgerEvent{TS: at, Session: fmt.Sprintf("past-%d", i), CumulativeUSD: v})
	}
}

func dollars(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i + 1)
	}
	return out
}

// With enough history and no budget of the operator's own, the session
// is measured against their p90 and the nudge says where that came from.
func TestEvaluate_DerivesBudgetFromHistory(t *testing.T) {
	dir := t.TempDir()
	seedHistory(t, dir, fixedNow.Add(-48*time.Hour), dollars(40)...) // p90 = $36
	cfg := DefaultConfig()

	// 40M cache-read on opus = $20, 56% of $36.
	tp := writeTranscript(t, dir, turnLine(ts(1), 40_000_000, opus))
	d := Evaluate(dir, "now", tp, cfg, fixedNow)
	if d.BudgetUSD != 36 {
		t.Fatalf("budget = %v, want the p90 of 40 sessions ($36)", d.BudgetUSD)
	}
	if !d.Nudge || !strings.Contains(d.Message, "your p90 session, from 40 sessions") {
		t.Fatalf("the nudge should name its provenance: nudge=%v %q", d.Nudge, d.Message)
	}

	b, ok := ReadSessionBudget(dir)
	if !ok || b.Source != learning.SourceHistory || b.Sessions != 40 || b.ObservedUSD != 36 {
		t.Fatalf("belief not recorded with its evidence: %+v ok=%v", b, ok)
	}
}

// Too little history keeps the fixed default, and says so.
func TestEvaluate_KeepsDefaultBelowMinSessions(t *testing.T) {
	dir := t.TempDir()
	seedHistory(t, dir, fixedNow.Add(-time.Hour), dollars(5)...)
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus)) // $30
	d := Evaluate(dir, "now", tp, DefaultConfig(), fixedNow)
	if d.BudgetUSD != DefaultBudgetUSD {
		t.Fatalf("budget = %v, want the $50 default", d.BudgetUSD)
	}
	if !strings.Contains(d.Message, "the default $50 session ceiling") {
		t.Fatalf("default wording changed: %q", d.Message)
	}
}

// A budget the operator set wins over anything history says.
func TestEvaluate_ConfiguredBudgetBeatsHistory(t *testing.T) {
	dir := t.TempDir()
	seedHistory(t, dir, fixedNow.Add(-time.Hour), dollars(40)...)
	cfg := DefaultConfig()
	cfg.BudgetUSD = 80
	tp := writeTranscript(t, dir, turnLine(ts(1), 90_000_000, opus)) // $45
	d := Evaluate(dir, "now", tp, cfg, fixedNow)
	if d.BudgetUSD != 80 || !strings.Contains(d.Message, "your $80 session budget") {
		t.Fatalf("configured budget lost: %v %q", d.BudgetUSD, d.Message)
	}
}

// The belief stands for a week; after that it is re-derived, and the
// move from last week's figure is clamped.
func TestSessionBudget_RefreshesWeeklyWithClampedDrift(t *testing.T) {
	dir := t.TempDir()
	seedHistory(t, dir, fixedNow.Add(-time.Hour), dollars(40)...)
	first := sessionBudget(dir, DefaultConfig(), fixedNow)
	if first.USD != 36 {
		t.Fatalf("first derivation = %v", first.USD)
	}

	// A burst of expensive sessions three days later does not move it...
	seedMore := func(at time.Time) {
		for i := 0; i < 200; i++ {
			appendLedger(dir, ledgerEvent{TS: at, Session: fmt.Sprintf("big-%d", i), CumulativeUSD: 400})
		}
	}
	seedMore(fixedNow.Add(72 * time.Hour))
	if got := sessionBudget(dir, DefaultConfig(), fixedNow.Add(72*time.Hour)).USD; got != 36 {
		t.Fatalf("re-derived before the week was out: %v", got)
	}
	// ...and a week on, it may only climb 25%.
	later := fixedNow.Add(8 * 24 * time.Hour)
	got := sessionBudget(dir, DefaultConfig(), later)
	if got.USD != 45 {
		t.Fatalf("drift not clamped: %v, want $45", got.USD)
	}
	b, _ := ReadSessionBudget(dir)
	if b.Clamp != learning.ClampDriftUp || b.PreviousUSD != 36 || b.ObservedUSD != 400 {
		t.Fatalf("clamp not recorded: %+v", b)
	}
}

// A refresh mid-session does not move the bar that session is judged by.
func TestEvaluate_PinsTheBudgetForTheSession(t *testing.T) {
	dir := t.TempDir()
	seedHistory(t, dir, fixedNow.Add(-time.Hour), dollars(40)...)
	tp := writeTranscript(t, dir, turnLine(ts(1), 2_000_000, opus))
	if d := Evaluate(dir, "now", tp, DefaultConfig(), fixedNow); d.BudgetUSD != 36 {
		t.Fatalf("budget = %v", d.BudgetUSD)
	}
	for i := 0; i < 200; i++ {
		appendLedger(dir, ledgerEvent{TS: fixedNow, Session: fmt.Sprintf("big-%d", i), CumulativeUSD: 400})
	}
	rewrite(t, tp, turnLine(ts(1), 2_000_000, opus), turnLine(ts(2), 2_000_000, opus))
	if d := Evaluate(dir, "now", tp, DefaultConfig(), fixedNow.Add(8*24*time.Hour)); d.BudgetUSD != 36 {
		t.Fatalf("the session's budget moved under it: %v", d.BudgetUSD)
	}
}
