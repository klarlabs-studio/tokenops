package coachhook

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func weeklyQuota(used float64, resetsIn time.Duration) *Quota {
	return &Quota{Provider: "anthropic", Window: plans.QuotaWindow{
		Label: "weekly", UsedPct: used, ResetsAt: fixedNow.Add(resetsIn), Duration: 7 * 24 * time.Hour,
	}}
}

// On a flat plan the dollar figure is a counterfactual and the quota window
// is what stops work, so with a live reading the coach speaks quota.
func TestQuotaReplacesDollarNudgesOnFlatPlans(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = weeklyQuota(76, 2*24*time.Hour)
	// $30 of $50 would fire the 50% dollar tier.
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if !d.Nudge || d.QuotaTier != 0.75 {
		t.Fatalf("want the 75%% quota tier, got %+v", d)
	}
	for _, want := range []string{"76%", "weekly", "resets in 2d"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q missing %q", d.Message, want)
		}
	}
	if strings.Contains(d.Message, "API-equivalent") {
		t.Errorf("quota nudge still talks dollars: %q", d.Message)
	}
	if d.FiredFraction != 0 {
		t.Errorf("dollar tier fired alongside quota: %v", d.FiredFraction)
	}
}

// A window spans sessions, so each quota tier is said once per window,
// not once per session.
func TestQuotaTierLatchesAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = weeklyQuota(76, 2*24*time.Hour)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("first session did not hear the 75% tier")
	}
	if d := Evaluate(dir, "s2", tp, cfg, fixedNow.Add(time.Hour)); d.Nudge {
		t.Fatalf("second session repeated the 75%% tier of the same window: %q", d.Message)
	}
	cfg.Quota = weeklyQuota(91, 2*24*time.Hour-time.Hour)
	d := Evaluate(dir, "s2", tp, cfg, fixedNow.Add(2*time.Hour))
	if !d.Nudge || d.QuotaTier != 0.90 {
		t.Fatalf("crossing 90%% should speak once, got %+v", d)
	}
}

// Meter readings jitter the reset by fractions of a second between polls;
// that is the same window. A reset days later is a new one.
func TestQuotaTiersRearmForANewWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	cfg.Quota = weeklyQuota(76, 2*24*time.Hour)
	Evaluate(dir, "s1", tp, cfg, fixedNow)
	cfg.Quota = weeklyQuota(77, 2*24*time.Hour+300*time.Millisecond)
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("reset jitter re-armed the tier: %q", d.Message)
	}
	cfg.Quota = weeklyQuota(76, 9*24*time.Hour)
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("a new window did not re-arm the 75% tier")
	}
}

// Pace turns a share into advice.
func TestQuotaNudgeSaysWhenPaceRunsOut(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = weeklyQuota(55, 5*24*time.Hour) // 55% two days in
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if !d.Nudge || !strings.Contains(d.Message, "this pace") {
		t.Fatalf("want a pace warning, got %+v", d)
	}
	dir = t.TempDir()
	cfg.Quota = weeklyQuota(55, 12*time.Hour) // 55% six and a half days in
	tp = writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	d = Evaluate(dir, "s1", tp, cfg, fixedNow)
	if strings.Contains(d.Message, "this pace") {
		t.Errorf("pace warning on a window that lasts to its reset: %q", d.Message)
	}
}

func TestQuotaExhaustedSaysSo(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = weeklyQuota(100, 30*time.Hour)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if !d.Nudge || d.QuotaTier != 1.0 || !strings.Contains(d.Message, "limit reached") || !strings.Contains(d.Message, "resets in 1d 6h") {
		t.Fatalf("exhausted window: %+v", d)
	}
}

// Observe mode records and says nothing, as for the dollar ladder.
func TestQuotaSilentWhenCoachingDisabled(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Enabled = false
	cfg.Quota = weeklyQuota(95, time.Hour)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("disabled coach spoke: %q", d.Message)
	}
}

func TestStatsCountQuotaNudges(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = weeklyQuota(76, 2*24*time.Hour)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	Evaluate(dir, "s1", tp, cfg, fixedNow)
	s, err := ReadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.QuotaNudges["75%"] != 1 || s.QuotaWindows["anthropic weekly"] != 1 {
		t.Fatalf("quota stats = %+v / %+v", s.QuotaNudges, s.QuotaWindows)
	}
	if s.Alerts != 0 {
		t.Errorf("a quota nudge counted as a dollar alert: %d", s.Alerts)
	}
}
