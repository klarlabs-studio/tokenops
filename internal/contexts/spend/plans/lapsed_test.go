package plans

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// fridayCodex is Codex's last reading before a weekend: the 5-hour window
// nearly used up and resetting that evening, the week half used and
// resetting on Wednesday.
func fridayCodex(friday time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{Source: "codex-jsonl", Timestamp: friday, Attributes: map[string]string{
		"primary_used_pct": "90.00", "primary_window_min": "300",
		"primary_resets_at":  strconv.FormatInt(friday.Add(2*time.Hour).Unix(), 10),
		"secondary_used_pct": "40.00", "secondary_window_min": "10080",
		"secondary_resets_at": strconv.FormatInt(friday.Add(5*24*time.Hour).Unix(), 10),
	}}
}

// A window whose reset has passed since its reading is over. Read as it
// was, a 5-hour window left at 90% on Friday still stood at 90% on Monday,
// with no reset, and pressed the plan.
func TestWindowPastItsResetIsLapsed(t *testing.T) {
	friday := time.Date(2026, 11, 6, 17, 0, 0, 0, time.UTC)
	monday := friday.Add(64 * time.Hour)
	r := authFakeReader{events: []*eventschema.Envelope{fridayCodex(friday)}}

	got := VendorWindows(context.Background(), r, eventschema.ProviderOpenAI, monday)
	by := map[string]VendorWindow{}
	for _, w := range got {
		by[w.Name] = w
	}
	session, week := by["5h"], by["week"]
	if !session.Lapsed || session.Current(monday) || !session.ResetsAt.IsZero() || session.ResetsIn != "" || session.Pace != nil {
		t.Errorf("the 5h window reset on Friday: %+v", session)
	}
	if week.Lapsed || !week.Current(monday) || week.ResetsIn == "" {
		t.Errorf("the week has not reset: %+v", week)
	}
	if p := WindowPressure("Codex", got, monday); p != nil {
		t.Errorf("a window that has reset presses nothing: %+v", p)
	}
	if p := BindingPrice("Codex", got, monday); p == nil || p.Window != "weekly" {
		t.Errorf("the week is the only window to price: %+v", p)
	}
	if w, ok := Busiest(got, monday); !ok || w.Name != "week" {
		t.Errorf("busiest current window %+v %v", w, ok)
	}

	// On Friday itself the same reading is current.
	then := VendorWindows(context.Background(), r, eventschema.ProviderOpenAI, friday.Add(time.Minute))
	if w, ok := Busiest(then, friday.Add(time.Minute)); !ok || w.Name != "5h" || w.Lapsed {
		t.Errorf("before its reset the 5h window is the busiest: %+v", then)
	}
}

// A window that was current when it was read stops being so once its
// reset passes, whoever still holds the reading.
func TestCurrentFollowsTheClock(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		w    VendorWindow
		want bool
	}{
		"resets later":    {VendorWindow{UsedPct: 50, ResetsAt: now.Add(time.Minute)}, true},
		"no reset known":  {VendorWindow{UsedPct: 50}, true},
		"reset just now":  {VendorWindow{UsedPct: 50, ResetsAt: now}, false},
		"reset before":    {VendorWindow{UsedPct: 50, ResetsAt: now.Add(-time.Minute)}, false},
		"lapsed":          {VendorWindow{UsedPct: 50, Lapsed: true}, false},
		"source stopped":  {VendorWindow{UsedPct: 50, Stale: true, ResetsAt: now.Add(time.Hour)}, false},
		"not started yet": {VendorWindow{NotStarted: true}, true},
	}
	for name, c := range cases {
		if got := c.w.Current(now); got != c.want {
			t.Errorf("%s: current %v, want %v", name, got, c.want)
		}
	}
}

// The risk and the advice follow the busiest window that is current: one
// that has reset says nothing about now, however full it was.
func TestLapsedWindowSetsNeitherRiskNorAdvice(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	windows := []VendorWindow{
		{Name: "5h", UsedPct: 96, Duration: 5 * time.Hour, Lapsed: true},
		{Name: "week", UsedPct: 40, Duration: week, ResetsAt: now.Add(48 * time.Hour)},
	}
	p, _ := Lookup("claude-ultra")
	if report := computeHeadroomFor(p, HeadroomInputs{Now: now, VendorWindows: windows}); report.OverageRisk != RiskLow || len(report.Windows) != 2 {
		t.Errorf("risk %s, windows %d", report.OverageRisk, len(report.Windows))
	}
	out, err := ComputeSessionBudget("claude-ultra", SessionBudgetInputs{Now: now, VendorWindows: windows})
	if err != nil {
		t.Fatal(err)
	}
	if out.WindowPct != 40 || out.RecommendedAction != ActionContinue || out.WindowResetsIn != "48h0m0s" || len(out.Windows) != 2 {
		t.Errorf("%+v", out)
	}

	// Nothing current: no share, and no advice made from an old one.
	gone := []VendorWindow{{Name: "5h", UsedPct: 96, Duration: 5 * time.Hour, Lapsed: true}}
	if report := computeHeadroomFor(p, HeadroomInputs{Now: now, VendorWindows: gone}); report.OverageRisk == RiskHigh {
		t.Errorf("a window that has reset set the risk: %s", report.OverageRisk)
	}
	out, err = ComputeSessionBudget("claude-ultra", SessionBudgetInputs{Now: now, VendorWindows: gone})
	if err != nil {
		t.Fatal(err)
	}
	if out.RecommendedAction != ActionUnknown || out.WindowPct != 0 || out.Confidence != ConfidenceLow || len(out.Windows) != 1 {
		t.Errorf("%+v", out)
	}
}

// The single-window reading the router and the session budget ask for is
// none once the window it measured is over: 90% used on Friday is not a
// reading of Monday's window.
func TestAuthoritativeWindowPastItsResetIsNoReading(t *testing.T) {
	friday := time.Date(2026, 11, 6, 17, 0, 0, 0, time.UTC)
	monday := friday.Add(64 * time.Hour)
	codex, _ := Lookup("gpt-plus")
	claude, _ := Lookup("claude-max")
	r := authFakeReader{events: []*eventschema.Envelope{
		fridayCodex(friday),
		// Read twenty minutes ago, five minutes before its reset.
		{Source: "claude-usage-meter", Timestamp: monday.Add(-20 * time.Minute), Attributes: map[string]string{
			"five_hour_used_pct": "88.00", "five_hour_reset_at": monday.Add(-15 * time.Minute).Format(time.RFC3339),
		}},
	}}
	if a := LatestAuthoritativeWindow(context.Background(), r, eventschema.ProviderOpenAI, codex, monday); a != nil {
		t.Errorf("codex: %+v", a)
	}
	if a := LatestAuthoritativeWindow(context.Background(), r, eventschema.ProviderAnthropic, claude, monday); a != nil {
		t.Errorf("claude: %+v", a)
	}
	// Before the reset the same readings stand.
	if a := LatestAuthoritativeWindow(context.Background(), r, eventschema.ProviderAnthropic, claude, monday.Add(-16*time.Minute)); a == nil || a.UsedPct != 88 || a.ResetsIn != time.Minute {
		t.Errorf("claude before its reset: %+v", a)
	}
	if a := LatestAuthoritativeWindow(context.Background(), r, eventschema.ProviderOpenAI, codex, friday.Add(time.Hour)); a == nil || a.UsedPct != 90 || a.ResetsIn != time.Hour {
		t.Errorf("codex before its reset: %+v", a)
	}
	// A reading that names no reset is kept: nothing says its window is over.
	bare := authFakeReader{events: []*eventschema.Envelope{{Source: "codex-jsonl", Timestamp: monday.Add(-time.Hour), Attributes: map[string]string{
		"primary_used_pct": "30.00", "primary_window_min": "300",
	}}}}
	if a := LatestAuthoritativeWindow(context.Background(), bare, eventschema.ProviderOpenAI, codex, monday); a == nil || a.UsedPct != 30 || a.ResetsIn != 0 {
		t.Errorf("a reading without a reset: %+v", a)
	}
}

// A monthly share read before the month rolled over is last month's.
func TestMonthlyReadingPastItsResetIsNoReading(t *testing.T) {
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	read := now.Add(-4 * 24 * time.Hour)
	copilot := authFakeReader{events: []*eventschema.Envelope{{Source: "copilot", Timestamp: read, Attributes: map[string]string{
		"percent_remaining": "5", "entitlement": "300", "quota_reset_date": "2026-11-01",
	}}}}
	if a := LatestAuthoritativeMonthly(context.Background(), copilot, eventschema.ProviderGitHub, now); a != nil {
		t.Errorf("copilot: %+v", a)
	}
	if a := LatestAuthoritativeMonthly(context.Background(), copilot, eventschema.ProviderGitHub, read); a == nil || a.UsedPct != 95 {
		t.Errorf("copilot before its reset: %+v", a)
	}
	cursor := authFakeReader{events: []*eventschema.Envelope{{Source: "cursor", Timestamp: read, Attributes: map[string]string{
		"used_pct": "95", "billing_cycle_end": "2026-11-01T00:00:00Z",
	}}}}
	if a := LatestAuthoritativeMonthly(context.Background(), cursor, eventschema.ProviderCursor, now); a != nil {
		t.Errorf("cursor: %+v", a)
	}
	if a := LatestAuthoritativeMonthly(context.Background(), cursor, eventschema.ProviderCursor, read); a == nil || a.UsedPct != 95 {
		t.Errorf("cursor before its reset: %+v", a)
	}
}

// An account reader's window lapses the same way.
func TestAccountWindowPastItsResetIsLapsed(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	// Read half an hour ago, twenty minutes before the 5h window reset.
	e := &eventschema.Envelope{Source: "vendor-account", Timestamp: now.Add(-30 * time.Minute), Payload: &eventschema.PromptEvent{Provider: "zai"},
		Attributes: map[string]string{
			"window_0_name": "5h", "window_0_used_pct": "80", "window_0_duration_min": "300",
			"window_0_reset_at": now.Add(-10 * time.Minute).Format(time.RFC3339),
			"window_1_name":     "week", "window_1_used_pct": "20", "window_1_duration_min": "10080",
			"window_1_reset_at": now.Add(72 * time.Hour).Format(time.RFC3339),
		}}
	got := VendorWindows(context.Background(), authFakeReader{events: []*eventschema.Envelope{e}}, "zai", now)
	if len(got) != 2 {
		t.Fatalf("windows %+v", got)
	}
	for _, w := range got {
		if lapsed := w.Name == "5h"; w.Lapsed != lapsed || w.Current(now) == lapsed {
			t.Errorf("%s: %+v", w.Name, w)
		}
	}
}

func TestResetIn(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		raw    string
		in     time.Duration
		lapsed bool
	}{
		"none":             {"", 0, false},
		"unreadable":       {"soon", 0, false},
		"unix ahead":       {strconv.FormatInt(now.Add(90*time.Minute).Unix(), 10), 90 * time.Minute, false},
		"unix past":        {strconv.FormatInt(now.Add(-time.Second).Unix(), 10), 0, true},
		"unix zero":        {"0", 0, false},
		"rfc3339 ahead":    {"2026-11-09T10:00:00.048431+00:00", time.Hour + 48431*time.Microsecond, false},
		"rfc3339 past":     {"2026-11-09T08:59:59Z", 0, true},
		"at this instant":  {"2026-11-09T09:00:00Z", 0, true},
		"plain date ahead": {"2026-12-01", 22*24*time.Hour - 9*time.Hour, false},
		"plain date past":  {"2026-11-01", 0, true},
	}
	for name, c := range cases {
		in, lapsed := resetIn(c.raw, now)
		if in != c.in || lapsed != c.lapsed {
			t.Errorf("%s: %v %v, want %v %v", name, in, lapsed, c.in, c.lapsed)
		}
	}
}
