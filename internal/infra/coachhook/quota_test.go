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

// quiet speaks only when work is about to stop: 90% and up, or earlier
// when the pace runs out before the reset.
func TestQuietSpeaksOnlyWhenWorkIsAboutToStop(t *testing.T) {
	tp := func(dir string) string { return writeTranscript(t, dir, turnLine(ts(1), 1_000, opus)) }

	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Verbosity = "quiet"
	cfg.Quota = weeklyQuota(76, 12*time.Hour) // 76%, lasts to the reset
	if d := Evaluate(dir, "s", tp(dir), cfg, fixedNow); d.Nudge {
		t.Fatalf("quiet spoke at 76%% with the window lasting: %q", d.Message)
	}
	cfg.Quota = weeklyQuota(92, 12*time.Hour)
	if d := Evaluate(dir, "s", tp(dir), cfg, fixedNow); !d.Nudge || d.QuotaTier != 0.90 {
		t.Fatalf("quiet stayed silent at 92%%: %+v", d)
	}

	dir = t.TempDir()
	cfg.Quota = weeklyQuota(55, 5*24*time.Hour) // runs out before the reset
	if d := Evaluate(dir, "s", tp(dir), cfg, fixedNow); !d.Nudge {
		t.Fatal("quiet stayed silent while the pace runs out before the reset")
	}
}

func TestQuietDollarLadderOnlyAtBudget(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Verbosity = "quiet"
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus)) // $30 of $50
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("quiet spoke at 60%% of the dollar budget: %q", d.Message)
	}
	rewrite(t, tp, turnLine(ts(1), 60_000_000, opus), turnLine(ts(2), 50_000_000, opus)) // $55
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("quiet stayed silent past the dollar budget")
	}
}

// verbose names every window and always says how the pace compares.
func TestVerboseExplainsTheWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Verbosity = "verbose"
	cfg.Quota = weeklyQuota(76, 12*time.Hour)
	cfg.Quota.All = []plans.QuotaWindow{
		{Label: "5-hour", UsedPct: 12, ResetsAt: fixedNow.Add(2 * time.Hour), Duration: 5 * time.Hour},
		cfg.Quota.Window,
	}
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	for _, want := range []string{"lasts to the reset", "5-hour 12%", "weekly 76%"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("verbose message missing %q: %q", want, d.Message)
		}
	}
}

func TestQuietHoldsBackThePromotionCase(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Verbosity = "quiet"
	cfg.Promotion = "tokenops: read-guard could refuse these re-reads"
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("quiet argued the read-guard case: %q", d.Message)
	}
}

func TestQuotaStatusNamesTheNextTip(t *testing.T) {
	now := time.Date(2026, 9, 29, 20, 0, 0, 0, time.UTC)
	q := &Quota{Provider: "anthropic", Window: plans.QuotaWindow{Label: "weekly", UsedPct: 45, ResetsAt: now.Add(74 * time.Hour)}}
	dir := t.TempDir()
	got := QuotaStatus(dir, q, "", now)
	if !strings.Contains(got, "weekly 45%") || !strings.Contains(got, "next tip at 50%") || !strings.Contains(got, "resets in") {
		t.Errorf("status = %q", got)
	}
	if got := QuotaStatus(dir, q, verbosityQuiet, now); !strings.Contains(got, "next tip at 90%") {
		t.Errorf("quiet status = %q; want the 90%% tier", got)
	}
	// A tier already said for this window is not promised again.
	saveQuotaLatch(dir, map[string]float64{quotaKey(q): 0.75}, now)
	q.Window.UsedPct = 80
	if got := QuotaStatus(dir, q, "", now); !strings.Contains(got, "next tip at 90%") {
		t.Errorf("after 75%% was said: %q", got)
	}
	saveQuotaLatch(dir, map[string]float64{quotaKey(q): 1.0}, now)
	q.Window.UsedPct = 99
	if got := QuotaStatus(dir, q, "", now); !strings.Contains(got, "already given") {
		t.Errorf("all tiers said: %q", got)
	}
	if QuotaStatus(dir, nil, "", now) != "" {
		t.Error("no reading produced a line")
	}
}
