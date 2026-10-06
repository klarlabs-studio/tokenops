package plans

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestVendorWindowsPerVendor(t *testing.T) {
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	meter := &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: now.Add(-10 * time.Minute), Attributes: map[string]string{
		"five_hour_used_pct": "13.00", "five_hour_reset_at": "2026-10-03T10:00:00.238665+00:00",
		"seven_day_used_pct": "85.00", "seven_day_reset_at": "2026-10-09T23:00:00+00:00",
		"weekly_scoped_fable_used_pct": "0.00", "weekly_scoped_fable_model_scope": "Fable",
	}}
	// Newer, and with keys the Claude parser would also match.
	codex := &eventschema.Envelope{Source: "codex-jsonl", Timestamp: now.Add(-time.Minute), Attributes: map[string]string{
		"primary_used_pct": "14.00", "primary_window_min": "10080", "primary_resets_at": "1791607286",
		"secondary_used_pct": "0.00", "secondary_window_min": "0", "secondary_resets_at": "0",
	}}
	r := authFakeReader{events: []*eventschema.Envelope{meter, codex}}

	claude := VendorWindows(context.Background(), r, eventschema.ProviderAnthropic, now)
	if len(claude) != 3 || claude[0].Name != "week" || claude[0].UsedPct != 85 || claude[1].Name != "5h" ||
		claude[1].ResetsIn != "1h0m0s" || claude[2].Name != "week (Fable)" {
		t.Errorf("claude windows %+v", claude)
	}
	openai := VendorWindows(context.Background(), r, eventschema.ProviderOpenAI, now)
	if len(openai) != 1 || openai[0].Name != "week" || openai[0].UsedPct != 14 {
		t.Errorf("codex windows %+v", openai)
	}
	if VendorWindows(context.Background(), r, eventschema.ProviderGitHub, now) != nil {
		t.Error("a vendor without windows has none")
	}
}

// A weekly window near its limit is the risk, however empty the 5-hour
// window is.
func TestBusiestVendorWindowSetsTheRisk(t *testing.T) {
	p, _ := Lookup("claude-max-20x")
	report := computeHeadroomFor(p, HeadroomInputs{
		Now:           time.Now(),
		VendorWindows: []VendorWindow{{Name: "week", UsedPct: 85}, {Name: "5h", UsedPct: 3}},
	})
	if report.OverageRisk != RiskHigh || len(report.Windows) != 2 {
		t.Errorf("risk %s windows %d", report.OverageRisk, len(report.Windows))
	}
}

// The session budget follows the busiest window the vendor reports.
func TestSessionBudgetFromVendorWindows(t *testing.T) {
	now := time.Now().UTC()
	out, err := ComputeSessionBudget("claude-max-20x", SessionBudgetInputs{
		Now: now,
		VendorWindows: []VendorWindow{
			{Name: "week", UsedPct: 87, Duration: 7 * 24 * time.Hour, ResetsAt: now.Add(48 * time.Hour)},
			{Name: "5h", UsedPct: 3, Duration: 5 * time.Hour, ResetsAt: now.Add(time.Hour)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.WindowPct != 87 || out.RecommendedAction != ActionSlowDown || out.WindowResetsIn != "48h0m0s" ||
		len(out.Windows) != 2 || out.WindowConsumed != 0 || out.WindowCap != 0 {
		t.Errorf("%+v", out)
	}
}

// Claude Code's status line reports the same windows as the claude.ai
// meter. The newer figure wins per window; a window only the meter reports
// is kept.
func TestVendorWindowsMergeTheMeterAndTheStatusLine(t *testing.T) {
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	meter := &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: now.Add(-2 * time.Hour), Attributes: map[string]string{
		"five_hour_used_pct": "13.00", "five_hour_reset_at": "2026-10-03T10:00:00Z",
		"weekly_scoped_fable_used_pct": "2.00", "weekly_scoped_fable_model_scope": "Fable",
	}}
	statusline := &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: now.Add(-time.Minute), Attributes: map[string]string{
		"five_hour_used_pct": "40.00", "five_hour_reset_at": "2026-10-03T10:00:00Z",
		"seven_day_used_pct": "9.00", "seven_day_reset_at": "2026-10-09T23:00:00Z",
		"granularity": "quota_snapshot",
	}}
	r := authFakeReader{events: []*eventschema.Envelope{statusline, meter}}

	got := VendorWindows(context.Background(), r, eventschema.ProviderAnthropic, now)
	if len(got) != 3 || got[0].Name != "5h" || got[0].UsedPct != 40 || got[1].Name != "week" || got[2].Name != "week (Fable)" {
		t.Errorf("windows %+v", got)
	}
}

// A window claude.ai reports under a name with no known meaning (an
// internal codename, no kind) is still a limit that can stop work: it is
// shown as "other limit" and keeps the vendor's own label, never the
// codename as if it were a window's name.
func TestUnknownClaudeWindowIsAnOtherLimit(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	meter := &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: now.Add(-time.Minute), Attributes: map[string]string{
		"seven_day_used_pct": "7.00", "seven_day_kind": "weekly_all", "seven_day_reset_at": "2026-10-09T23:00:00Z",
		"iguana_necktie_used_pct": "40.00", "iguana_necktie_reset_at": "2026-11-05T07:59:00+00:00",
	}}
	got := VendorWindows(context.Background(), authFakeReader{events: []*eventschema.Envelope{meter}}, eventschema.ProviderAnthropic, now)
	if len(got) != 2 || got[0].Name != OtherLimit || got[0].VendorLabel != "iguana_necktie" || got[0].UsedPct != 40 ||
		got[0].ResetsAt.IsZero() || got[1].Name != "week" || got[1].VendorLabel != "" {
		t.Errorf("windows %+v", got)
	}
}

// A Claude apps gateway spend limit is a window, named by its period, and
// a monthly one with dollars is the vendor's spend.
func TestStatusLineSpendLimit(t *testing.T) {
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	reading := &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: now.Add(-time.Minute),
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		Attributes: map[string]string{
			"spend_limit_used_pct": "62.80", "spend_limit_reset_at": "2026-11-01T00:00:00Z", "spend_limit_period": "monthly",
			"extra_usage_used": "314.12", "extra_usage_limit": "500.00", "extra_usage_currency": "USD",
			"extra_usage_limit_reached": "false", "granularity": "quota_snapshot",
		}}
	r := authFakeReader{events: []*eventschema.Envelope{reading}}

	got := VendorWindows(context.Background(), r, eventschema.ProviderAnthropic, now)
	if len(got) != 1 || got[0].Name != "monthly spend limit" || got[0].UsedPct != 62.8 {
		t.Errorf("windows %+v", got)
	}
	spend := LatestVendorSpend(context.Background(), r, eventschema.ProviderAnthropic, now)
	if spend == nil || spend.UsedUSD != 314.12 || spend.LimitUSD != 500 || spend.Source != "claude_code_statusline:spend_limit" {
		t.Errorf("spend %+v", spend)
	}
	quota := QuotaWindowsFromAttributes(eventschema.ProviderAnthropic, reading.Attributes)
	if len(quota) != 1 || quota[0].Label != "monthly spend limit" {
		t.Errorf("quota windows %+v", quota)
	}
}
