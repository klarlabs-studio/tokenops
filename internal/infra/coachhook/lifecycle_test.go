package coachhook

import (
	"testing"
	"time"
)

func idCfg() Config {
	cfg := DefaultConfig()
	n := 0
	cfg.NewID = func() string { n++; return "tip-" + itoa(int64(n)) }
	return cfg
}

// Past the budget the coach speaks once; each further step over goes to
// the status line instead of interrupting again.
func TestEvaluate_OverBudgetSpeaksOncePerEpisode(t *testing.T) {
	dir := t.TempDir()
	cfg := idCfg()
	tp := writeTranscript(t, dir, turnLine(ts(1), 100_000_000, opus)) // $50, 100%
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge || !approx(d.FiredFraction, 1.0) {
		t.Fatalf("100%% should speak: %+v", d)
	}
	rewrite(t, tp, turnLine(ts(1), 100_000_000, opus), turnLine(ts(2), 100_000_000, opus)) // 200%
	d := Evaluate(dir, "s", tp, cfg, fixedNow.Add(time.Minute))
	if d.Nudge {
		t.Fatalf("200%% interrupted again: %q", d.Message)
	}
	if !d.Ambient || d.OfferID == "" || d.TipKind != "budget_over" || d.Suppressed != SuppressedEpisode {
		t.Fatalf("200%% should go to the status line as the open tip: %+v", d)
	}
	if tip, ok := OpenTip(dir, "s"); !ok || tip.ID != d.OfferID {
		t.Fatalf("status line has no tip to show: %+v %v", tip, ok)
	}
	// And it latched: the same 200% is not offered again.
	if again := Evaluate(dir, "s", tp, cfg, fixedNow.Add(2*time.Minute)); again.Ambient || again.Nudge {
		t.Fatalf("offered twice: %+v", again)
	}
}

// An answered tip closes the episode: later tiers of the family in the
// session are not said at all, and stay latched.
func TestEvaluate_AnsweredEpisodeHoldsLaterTiers(t *testing.T) {
	dir := t.TempDir()
	cfg := idCfg()
	cfg.Held = func(session, kind string) string {
		if session == "s" && kind == "budget_75" {
			return "answered"
		}
		return ""
	}
	tp := writeTranscript(t, dir, turnLine(ts(1), 80_000_000, opus)) // $40, 80%
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if d.Nudge || d.Ambient || d.Suppressed != "answered" {
		t.Fatalf("an answered episode spoke: %+v", d)
	}
	cfg.Held = nil
	rewrite(t, tp, turnLine(ts(1), 80_000_000, opus), turnLine(ts(2), 2_000_000, opus))
	if again := Evaluate(dir, "s", tp, cfg, fixedNow.Add(time.Minute)); again.Nudge {
		t.Fatalf("the held 75%% tier came back: %q", again.Message)
	}
}

// A snooze defers: once it ends, the tier speaks.
func TestEvaluate_SnoozeDefers(t *testing.T) {
	dir := t.TempDir()
	cfg := idCfg()
	until := fixedNow.Add(time.Hour)
	cfg.Held = func(_, kind string) string { return "" }
	snoozed := func(now time.Time) func(string, string) string {
		return func(string, string) string {
			if now.Before(until) {
				return "snoozed"
			}
			return ""
		}
	}
	cfg.Held = snoozed(fixedNow)
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus)) // 60%
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge || d.Suppressed != "snoozed" {
		t.Fatalf("snoozed tip spoke: %+v", d)
	}
	later := fixedNow.Add(2 * time.Hour)
	cfg.Held = snoozed(later)
	if d := Evaluate(dir, "s", tp, cfg, later); !d.Nudge || !approx(d.FiredFraction, 0.5) {
		t.Fatalf("tip did not speak after the snooze: %+v", d)
	}
}

// The daily interruption budget: past it, tips go to the status line;
// tips about work about to stop still speak; the next day starts afresh.
func TestEvaluate_DailyInterruptionBudget(t *testing.T) {
	dir := t.TempDir()
	cfg := idCfg()
	cfg.Quiet.MaxPerDay = 1
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus)) // 60%
	if d := Evaluate(dir, "a", tp, cfg, fixedNow); !d.Nudge {
		t.Fatalf("first interruption of the day held: %+v", d)
	}
	d := Evaluate(dir, "b", tp, cfg, fixedNow.Add(time.Minute))
	if d.Nudge || !d.Ambient || d.Suppressed != SuppressedDailyBudget || d.OfferID == "" {
		t.Fatalf("over the daily budget should go to the status line: %+v", d)
	}
	big := writeTranscript(t, t.TempDir(), turnLine(ts(1), 110_000_000, opus)) // 110%
	if d := Evaluate(dir, "c", big, cfg, fixedNow.Add(2*time.Minute)); !d.Nudge {
		t.Fatalf("the budget spent should still speak: %+v", d)
	}
	if d := Evaluate(dir, "d", tp, cfg, fixedNow.Add(24*time.Hour)); !d.Nudge {
		t.Fatalf("a new day did not reset the budget: %+v", d)
	}
	s, _ := ReadStats(dir)
	if s.Suppressed[SuppressedDailyBudget] != 1 {
		t.Errorf("stats = %v", s.Suppressed)
	}
}
