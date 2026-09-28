package coachhook

import (
	"fmt"
	"testing"
)

func tipConfig() Config {
	cfg := DefaultConfig()
	n := 0
	cfg.NewID = func() string { n++; return fmt.Sprint("tip-", n) }
	return cfg
}

// A tip is followed when the context shrinks to half or less afterwards:
// the operator compacted.
func TestTipFollowedByCompaction(t *testing.T) {
	dir := t.TempDir()
	cfg := tipConfig()
	// 60M cache-read on opus is $30 of $50: the 50% tier.
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !d.Nudge || d.OfferID != "tip-1" || d.TipKind != "budget_50" {
		t.Fatalf("first Stop: nudge=%v offer=%q kind=%q", d.Nudge, d.OfferID, d.TipKind)
	}
	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 20_000, opus))
	d = Evaluate(dir, "s", tp, cfg, fixedNow)
	if len(d.Resolved) != 1 || !d.Resolved[0].Followed || d.Resolved[0].ID != "tip-1" {
		t.Fatalf("after compaction: Resolved = %+v; want tip-1 followed", d.Resolved)
	}
}

// A tip nothing changes after is ignored once its window has passed.
func TestTipIgnoredAfterTheWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := tipConfig()
	// One tier only, so the later turns' spend cannot give a second tip.
	cfg.Tiers, cfg.OverBudgetStep = []float64{0.50}, 0
	lines := make([]string, 0, TipWindow+2)
	lines = append(lines, turnLine(ts(1), 60_000_000, opus))
	tp := writeTranscript(t, dir, lines...)
	Evaluate(dir, "s", tp, cfg, fixedNow)
	resolved := make([]TipResolution, 0, 1)
	for i := range TipWindow + 1 {
		lines = append(lines, turnLine(ts(2+i), 50_000_000, opus))
		rewrite(t, tp, lines...)
		resolved = append(resolved, Evaluate(dir, "s", tp, cfg, fixedNow).Resolved...)
	}
	if len(resolved) != 1 || resolved[0].Followed {
		t.Fatalf("Resolved = %+v; want one ignored", resolved)
	}
}

// A Stop with no new turn does not use up a tip's window.
func TestStopsWithoutNewTurnsDoNotCount(t *testing.T) {
	dir := t.TempDir()
	cfg := tipConfig()
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	Evaluate(dir, "s", tp, cfg, fixedNow)
	for range TipWindow + 1 {
		if d := Evaluate(dir, "s", tp, cfg, fixedNow); len(d.Resolved) != 0 {
			t.Fatalf("resolved without a new turn: %+v", d.Resolved)
		}
	}
}

// Early tiers ignored before go quiet; the spent budget never does, and
// verbose still speaks.
func TestQuietedTipsAreHeldBack(t *testing.T) {
	quiet := func(string) bool { return true }
	dir := t.TempDir()
	cfg := tipConfig()
	cfg.Quieted = quiet
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if d.Nudge || d.Suppressed != "ignored_before" || d.OfferID != "" {
		t.Fatalf("50%% tier: nudge=%v suppressed=%q offer=%q; want held back", d.Nudge, d.Suppressed, d.OfferID)
	}
	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 60_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge || d.TipKind != "budget_over" {
		t.Fatalf("spent budget: nudge=%v kind=%q; want spoken", d.Nudge, d.TipKind)
	}

	dir = t.TempDir()
	cfg.Verbosity = verbosityVerbose
	tp = writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("verbose held back a quieted tip")
	}
}

// A new tier arriving while a tip is open closes the open one as ignored.
func TestALaterTipClosesTheOpenOne(t *testing.T) {
	dir := t.TempDir()
	cfg := tipConfig()
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	Evaluate(dir, "s", tp, cfg, fixedNow)
	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 60_000_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if d.OfferID != "tip-2" || len(d.Resolved) != 1 || d.Resolved[0].ID != "tip-1" || d.Resolved[0].Followed {
		t.Fatalf("offer=%q Resolved=%+v; want tip-2 opened and tip-1 ignored", d.OfferID, d.Resolved)
	}
}
