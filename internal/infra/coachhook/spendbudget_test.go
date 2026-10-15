package coachhook

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
)

// teamDaily is a $10-a-day window of the operator's "team" budget, used
// pct percent.
func teamDaily(pct float64) boundaryledger.BudgetReading {
	start := fixedNow.UTC().Truncate(24 * time.Hour)
	return boundaryledger.BudgetReading{
		Key: "team:daily", Budget: "team", Window: "daily", Mode: "soft", Basis: "spend",
		Used: pct / 10, Limit: 10, UsedPct: pct, WarnPct: 75, CritPct: 95,
		WindowStart: start, ResetsAt: start.Add(24 * time.Hour),
	}
}

// spendCfg is the shipping config with the operator's budget windows: a
// session far below its own dollar tiers, so only the budget speaks.
func spendCfg(readings ...boundaryledger.BudgetReading) Config {
	c := DefaultConfig()
	c.Budgets = readings
	return c
}

func cheapTurn(t *testing.T, dir string) string {
	t.Helper()
	return writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
}

func TestBudgetWindowTipsOncePerTierPerWindow(t *testing.T) {
	dir := t.TempDir()
	tp := cheapTurn(t, dir)

	dec := Evaluate(dir, "s1", tp, spendCfg(teamDaily(82)), fixedNow)
	if !dec.Nudge || tipKind(dec) != "spend_75" {
		t.Fatalf("at 82%%: nudge %v kind %q; want the 75%% tier", dec.Nudge, tipKind(dec))
	}
	if !strings.Contains(dec.Message, "team (daily) is 82% used") || !strings.Contains(dec.Message, "$8.20 of $10.00") {
		t.Errorf("message = %q", dec.Message)
	}
	// Said once per window, across sessions.
	if again := Evaluate(dir, "s2", cheapTurn(t, t.TempDir()), spendCfg(teamDaily(85)), fixedNow.Add(time.Hour)); again.Nudge {
		t.Fatalf("the same tier spoke again: %q", again.Message)
	}
	over := Evaluate(dir, "s2", tp, spendCfg(teamDaily(104)), fixedNow.Add(2*time.Hour))
	if !over.Nudge || tipKind(over) != "spend_over" || !strings.Contains(over.Message, "only notifies") {
		t.Fatalf("at 104%%: %+v", over)
	}
	// Tomorrow is a new window.
	next := teamDaily(80)
	next.WindowStart, next.ResetsAt = next.WindowStart.Add(24*time.Hour), next.ResetsAt.Add(24*time.Hour)
	if tomorrow := Evaluate(dir, "s3", tp, spendCfg(next), fixedNow.Add(26*time.Hour)); !tomorrow.Nudge {
		t.Fatal("a new window's first tier did not speak")
	}
}

// Budget tips share the coach's lifecycle: past the day's interruption
// budget an early tier waits on the status line, and the limit itself
// still interrupts.
func TestBudgetWindowTipsKeepTheInterruptionBudget(t *testing.T) {
	dir := t.TempDir()
	tp := cheapTurn(t, dir)
	countInterruption(dir, fixedNow)
	cfg := spendCfg(teamDaily(80))
	cfg.Quiet.MaxPerDay = 1
	cfg.NewID = func() string { return "tip-1" }
	dec := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if dec.Nudge || !dec.Ambient || dec.Suppressed != SuppressedDailyBudget {
		t.Fatalf("past the day's budget: nudge %v ambient %v suppressed %q", dec.Nudge, dec.Ambient, dec.Suppressed)
	}
	tip, ok := OpenTip(dir, "s1")
	if !ok || tip.Short(fixedNow) != "75% of team daily budget used" {
		t.Errorf("status line = %q (%v)", tip.Short(fixedNow), ok)
	}
	cfg.Budgets = []boundaryledger.BudgetReading{teamDaily(100)}
	if over := Evaluate(dir, "s1", tp, cfg, fixedNow.Add(time.Hour)); !over.Nudge {
		t.Fatalf("the spent budget waited: %+v", over)
	}
}

func TestSnoozedBudgetTipsWait(t *testing.T) {
	dir := t.TempDir()
	tp := cheapTurn(t, dir)
	cfg := spendCfg(teamDaily(80))
	cfg.Held = func(_, kind string) string {
		if strings.HasPrefix(kind, "spend_") {
			return SuppressedSnoozed
		}
		return ""
	}
	if dec := Evaluate(dir, "s1", tp, cfg, fixedNow); dec.Nudge || dec.Suppressed != SuppressedSnoozed {
		t.Fatalf("snoozed: nudge %v suppressed %q", dec.Nudge, dec.Suppressed)
	}
	cfg.Held = nil
	if dec := Evaluate(dir, "s1", tp, cfg, fixedNow.Add(time.Hour)); !dec.Nudge {
		t.Fatal("the snoozed tier was dropped instead of deferred")
	}
}

func TestHardWindowTipNamesItsBoundary(t *testing.T) {
	dir := t.TempDir()
	r := teamDaily(96)
	r.Mode, r.Rule = "hard", "budget-team-daily"
	dec := Evaluate(dir, "s1", cheapTurn(t, dir), spendCfg(r), fixedNow)
	if !dec.Nudge || !strings.Contains(dec.Message, "tokenops boundaries promote budget-team-daily") {
		t.Fatalf("shadow hard window: %q", dec.Message)
	}
	r.Enforced = true
	r.Key = "other"
	dec = Evaluate(dir, "s1", cheapTurn(t, dir), spendCfg(r), fixedNow)
	if !strings.Contains(dec.Message, "boundary budget-team-daily asks before each further step") {
		t.Fatalf("enforced hard window: %q", dec.Message)
	}
}

// A provider budget's tip says whose bill it counts, so it is not read as
// all spend.
func TestProviderBudgetTipNamesTheProvider(t *testing.T) {
	dir := t.TempDir()
	r := teamDaily(80)
	r.Provider = "fireworks"
	dec := Evaluate(dir, "s1", cheapTurn(t, dir), spendCfg(r), fixedNow)
	if !dec.Nudge || !strings.Contains(dec.Message, "budget team (daily, billed by fireworks) is 80% used") {
		t.Fatalf("provider budget tip: %q", dec.Message)
	}
}

func TestQuietVerbositySpeaksOnlyAtTheLimit(t *testing.T) {
	dir := t.TempDir()
	cfg := spendCfg(teamDaily(96))
	cfg.Verbosity = verbosityQuiet
	if dec := Evaluate(dir, "s1", cheapTurn(t, dir), cfg, fixedNow); dec.Nudge {
		t.Fatalf("quiet spoke at 96%%: %q", dec.Message)
	}
}
