package coachhook

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func weeklyQuota(used float64, resetsIn time.Duration) *Quota {
	w := plans.QuotaWindow{Label: "weekly", UsedPct: used, ResetsAt: fixedNow.Add(resetsIn), Duration: 7 * 24 * time.Hour}
	return &Quota{Provider: "anthropic", Window: w, All: []plans.QuotaWindow{w}}
}

// burningQuota is a 5-hour window both look-backs run out runsOutIn from
// now, before its reset resetsIn from now.
func burningQuota(used float64, resetsIn, runsOutIn time.Duration) *Quota {
	w := plans.QuotaWindow{Label: "5-hour", UsedPct: used, ResetsAt: fixedNow.Add(resetsIn), Duration: 5 * time.Hour}
	return &Quota{Provider: "anthropic", Window: w, All: []plans.QuotaWindow{w}, Burn: plans.BurnVerdict{
		Warnings: []plans.BurnWarning{{Window: w, RunsOutAt: fixedNow.Add(runsOutIn), LongBurn: 1.5, ShortBurn: 1.6}},
	}}
}

// A share alone says nothing about running out: 76% two days before the
// reset with no burn forecast is not worth a word. The old fixed tiers
// spoke here.
func TestQuotaShareAloneSaysNothing(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = weeklyQuota(76, 2*24*time.Hour)
	// $30 of $50 would fire the 50% dollar tier on a metered plan.
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if d.Nudge || d.FiredFraction != 0 {
		t.Fatalf("spoke without a run-out forecast: %+v", d)
	}
}

// When both look-backs run the window out before its reset, the coach says
// when, how long before the reset, and what stretches it, in plan terms.
func TestBurnWarningSaysWhenItRunsOut(t *testing.T) {
	dir := t.TempDir()
	cfg := tipConfig()
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if !d.Nudge || !d.QuotaBurn || d.TipKind != burnKind {
		t.Fatalf("want a run-out warning, got %+v", d)
	}
	for _, want := range []string{"5-hour Claude limit", "runs out in ~1h 20m", "40m before it resets", "40% left"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q missing %q", d.Message, want)
		}
	}
	if strings.Contains(d.Message, "API-equivalent") || d.FiredFraction != 0 {
		t.Errorf("run-out warning talks dollars: %+v", d)
	}
}

// A window spans sessions, so its warning is said once per window, not
// once per session.
func TestBurnWarningOncePerWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("first session heard nothing")
	}
	cfg.Quota = burningQuota(70, 2*time.Hour+200*time.Millisecond, 50*time.Minute)
	if d := Evaluate(dir, "s2", tp, cfg, fixedNow.Add(10*time.Minute)); d.Nudge {
		t.Fatalf("second session repeated the warning for the same window: %q", d.Message)
	}
	cfg.Quota = burningQuota(40, 5*time.Hour, time.Hour) // a new window
	if d := Evaluate(dir, "s2", tp, cfg, fixedNow.Add(3*time.Hour)); !d.Nudge {
		t.Fatal("a new window did not re-arm the warning")
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
	if d := Evaluate(dir, "s2", tp, cfg, fixedNow.Add(time.Minute)); d.Nudge {
		t.Fatalf("the used-up window was said twice: %q", d.Message)
	}
}

// Observe mode records and says nothing, as for the dollar ladder.
func TestQuotaSilentWhenCoachingDisabled(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Enabled = false
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); d.Nudge {
		t.Fatalf("disabled coach spoke: %q", d.Message)
	}
}

func TestStatsCountBurnWarnings(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	Evaluate(dir, "s1", tp, cfg, fixedNow)
	s, err := ReadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.BurnWarnings != 1 || s.QuotaWindows["anthropic 5-hour"] != 1 {
		t.Fatalf("stats = %d / %+v", s.BurnWarnings, s.QuotaWindows)
	}
	if s.Alerts != 0 {
		t.Errorf("a quota warning counted as a dollar alert: %d", s.Alerts)
	}
}

// Precision is measurable: once a window resets, the coach records whether
// it was forecast to run out and whether it did.
func TestRunOutForecastsAreScored(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))

	// Window 1: forecast, then ran out.
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	Evaluate(dir, "s1", tp, cfg, fixedNow)
	cfg.Quota = weeklyQuota(0, 0)
	cfg.Quota.Window = plans.QuotaWindow{Label: "5-hour", UsedPct: 100, ResetsAt: fixedNow.Add(2 * time.Hour), Duration: 5 * time.Hour}
	cfg.Quota.All = []plans.QuotaWindow{cfg.Quota.Window}
	Evaluate(dir, "s1", tp, cfg, fixedNow.Add(90*time.Minute))

	// Window 2: forecast, but the pace eased and it lasted.
	cfg.Quota = burningQuota(30, 7*time.Hour, 3*time.Hour)
	Evaluate(dir, "s1", tp, cfg, fixedNow.Add(2*time.Hour+15*time.Minute))

	// Window 3: never forecast, yet ran out. Settling it also settles 2.
	w3 := plans.QuotaWindow{Label: "5-hour", UsedPct: 100, ResetsAt: fixedNow.Add(12 * time.Hour), Duration: 5 * time.Hour}
	cfg.Quota = &Quota{Provider: "anthropic", Window: w3, All: []plans.QuotaWindow{w3}}
	Evaluate(dir, "s1", tp, cfg, fixedNow.Add(8*time.Hour))
	cfg.Quota = burningQuota(5, 20*time.Hour, 30*time.Hour)
	cfg.Quota.Burn = plans.BurnVerdict{}
	Evaluate(dir, "s1", tp, cfg, fixedNow.Add(13*time.Hour))

	s, err := ReadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := RunOutScore{Windows: 3, Forecast: 2, ForecastRanOut: 1, RanOutUnforecast: 1}
	if s.RunOut != want {
		t.Fatalf("run-out score = %+v, want %+v", s.RunOut, want)
	}
	if p, ok := s.RunOut.Precision(); !ok || p != 0.5 {
		t.Errorf("precision = %v, %v", p, ok)
	}
}

// Quiet speaks the run-out warning: it is about work about to stop.
func TestQuietSpeaksTheRunOutWarning(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Verbosity = "quiet"
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !d.Nudge {
		t.Fatal("quiet stayed silent while the window runs out before the reset")
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

// verbose names every window and both look-backs' pace.
func TestVerboseExplainsTheWarning(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Verbosity = "verbose"
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	cfg.Quota.All = append(cfg.Quota.All, plans.QuotaWindow{Label: "weekly", UsedPct: 31, ResetsAt: fixedNow.Add(72 * time.Hour), Duration: 7 * 24 * time.Hour})
	tp := writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	for _, want := range []string{"1.5× the even pace over the last hour", "1.6× over the last 15 minutes", "5-hour 40% left", "weekly 69% left"} {
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

func TestQuotaStatusSaysWhatTheCoachWatches(t *testing.T) {
	dir := t.TempDir()
	q := weeklyQuota(45, 74*time.Hour)
	got := QuotaStatus(dir, q, fixedNow)
	if !strings.Contains(got, "weekly 55% left") || !strings.Contains(got, "resets in 3d 2h") || !strings.Contains(got, "not on course to run out") {
		t.Errorf("status = %q", got)
	}
	q = burningQuota(60, 2*time.Hour, 80*time.Minute)
	if got := QuotaStatus(dir, q, fixedNow); !strings.Contains(got, "5-hour runs out in ~1h 20m at this pace, 40m before the reset") {
		t.Errorf("burning status = %q", got)
	}
	q.Burn = plans.BurnVerdict{Stale: true}
	if got := QuotaStatus(dir, q, fixedNow); !strings.Contains(got, "reading too old to forecast") {
		t.Errorf("stale status = %q", got)
	}
	if QuotaStatus(dir, nil, fixedNow) != "" {
		t.Error("no reading produced a line")
	}
}
