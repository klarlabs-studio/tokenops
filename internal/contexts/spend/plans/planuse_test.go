package plans

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestPlanUseFrom(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-7 * 24 * time.Hour)
	reading := func(at time.Time, fiveUsed, fiveReset, weekUsed, weekReset string) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: at, Attributes: map[string]string{
			"granularity":    "quota_snapshot",
			"five_hour_kind": "session", "five_hour_used_pct": fiveUsed, "five_hour_reset_at": fiveReset,
			"seven_day_kind": "weekly_all", "seven_day_used_pct": weekUsed, "seven_day_reset_at": weekReset,
		}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	}
	turn := func(provider eventschema.Provider, cs eventschema.CostSource, tokens int64) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "claude-code-jsonl", Timestamp: now.Add(-time.Hour),
			Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload:    &eventschema.PromptEvent{Provider: provider, CostSource: cs, TotalTokens: tokens}}
	}
	rfc := func(t time.Time) string { return t.Format(time.RFC3339) }
	week1 := now.Add(-3 * 24 * time.Hour) // a weekly window that reset three days ago
	week2 := now.Add(4 * 24 * time.Hour)  // the one still open
	five1, five2 := now.Add(-50*time.Hour), now.Add(-20*time.Hour)
	envs := []*eventschema.Envelope{
		reading(now.Add(-54*time.Hour), "20.00", rfc(five1), "50.00", rfc(week1)),
		reading(now.Add(-51*time.Hour), "70.00", rfc(five1.Add(time.Second)), "62.00", rfc(week1)),
		reading(now.Add(-22*time.Hour), "30.00", rfc(five2), "10.00", rfc(week2)),
		reading(now.Add(-time.Hour), "0.00", "", "30.00", rfc(week2)),
		turn(eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded, 900),
		turn(eventschema.ProviderAnthropic, eventschema.CostSourceMetered, 100), // through a gateway
		turn("openrouter", eventschema.CostSourceMetered, 500),
		turn(eventschema.ProviderAnthropic, eventschema.CostSourceTrial, 1_000),
		// A reading from before the period does not count.
		reading(since.Add(-time.Hour), "99.00", rfc(since.Add(time.Hour)), "99.00", rfc(since.Add(time.Hour))),
	}
	r := PlanUseFrom(envs, since, now)
	a, ok := r.For("anthropic")
	if !ok {
		t.Fatalf("no anthropic figures: %+v", r)
	}
	if a.UtilizationPct == nil || *a.UtilizationPct != 62 || a.UtilizationWindow != "weekly" {
		t.Errorf("utilization = %v %q, want the reset weekly window's 62%% peak", deref(a.UtilizationPct), a.UtilizationWindow)
	}
	want := map[string]WindowUse{"5-hour": {"5-hour", 2, 50}, "weekly": {"weekly", 1, 62}}
	if len(a.Windows) != 2 || a.Windows[0] != want["5-hour"] || a.Windows[1] != want["weekly"] {
		t.Errorf("windows = %+v", a.Windows)
	}
	if a.CoveragePct == nil || *a.CoveragePct != 90 || a.PlanTokens != 900 || a.BilledTokens != 100 {
		t.Errorf("coverage = %v (%d/%d), want 90%%", deref(a.CoveragePct), a.PlanTokens, a.BilledTokens)
	}
	o, ok := r.For("openrouter")
	if !ok || o.CoveragePct == nil || *o.CoveragePct != 0 || o.UtilizationPct != nil {
		t.Errorf("openrouter = %+v", o)
	}
	if r.CoveragePct == nil || *r.CoveragePct != 60 {
		t.Errorf("all-provider coverage = %v, want 900 of 1500", deref(r.CoveragePct))
	}
}

// Nothing measured is nothing reported, not a zero.
func TestPlanUseFromNothing(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	r := PlanUseFrom(nil, now.Add(-7*24*time.Hour), now)
	if len(r.Providers) != 0 || r.CoveragePct != nil {
		t.Fatalf("report = %+v", r)
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
