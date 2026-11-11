package plans

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// stamped is a reading that says it stands for goodFor.
func stamped(source string, at time.Time, goodFor time.Duration, attrs map[string]string) *eventschema.Envelope {
	attrs[heartbeat.Attr] = strconv.Itoa(int(goodFor / time.Minute))
	return &eventschema.Envelope{Type: eventschema.EventTypePrompt, Source: source, Timestamp: at, Attributes: attrs}
}

// of is a reading for provider.
func of(provider eventschema.Provider, e *eventschema.Envelope) *eventschema.Envelope {
	e.Payload = &eventschema.PromptEvent{Provider: provider}
	return e
}

func TestReadingStale(t *testing.T) {
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	reading := func(source string, age time.Duration, attrs map[string]string) *eventschema.Envelope {
		return &eventschema.Envelope{Source: source, Timestamp: now.Add(-age), Attributes: attrs}
	}
	says := func(minutes string) map[string]string { return map[string]string{heartbeat.Attr: minutes} }
	for name, tc := range map[string]struct {
		e    *eventschema.Envelope
		want bool
	}{
		"says 25m, 24m old":                    {reading("claude-usage-meter", 24*time.Minute, says("25")), false},
		"says 25m, exactly 25m old":            {reading("claude-usage-meter", 25*time.Minute, says("25")), false},
		"says 25m, 26m old":                    {reading("claude-usage-meter", 26*time.Minute, says("25")), true},
		"says a day, 20h old":                  {reading("bedrock", 20*time.Hour, says("1495")), false},
		"says a day, 2 days old":               {reading("bedrock", 48*time.Hour, says("1495")), true},
		"a reader nobody listed, saying 16m":   {reading("some-new-reader", 17*time.Minute, says("16")), true},
		"status line, days old":                {reading("claude-code-statusline", 72*time.Hour, nil), false},
		"codex rollout, days old":              {reading("codex-jsonl", 72*time.Hour, nil), false},
		"status line that says, past its word": {reading("claude-code-statusline", time.Hour, says("16")), true},
		"polled, does not say, 59m old":        {reading("claude-usage-meter", 59*time.Minute, nil), false},
		"polled, does not say, 61m old":        {reading("claude-usage-meter", 61*time.Minute, nil), true},
		"no source, does not say, 61m old":     {reading("", 61*time.Minute, nil), true},
		"unreadable word, polled, 61m old":     {reading("zai", 61*time.Minute, says("soon")), true},
		"unreadable word, polled, 5m old":      {reading("zai", 5*time.Minute, says("soon")), false},
		"read a moment ahead of the clock":     {reading("claude-usage-meter", -time.Minute, says("25")), false},
		"no reading":                           {nil, false},
	} {
		if got := ReadingStale(tc.e, now); got != tc.want {
			t.Errorf("%s: stale %v, want %v", name, got, tc.want)
		}
	}
}

// A window from any reader that polls on a schedule is stale once the
// reading is older than it says it stands for. Only three sources had a
// limit, so an account reader's window stood as current for two weeks
// after the reader stopped.
func TestAccountWindowIsStaleOnceItsReaderStops(t *testing.T) {
	read := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	e := of("zai", stamped("zai", read, 55*time.Minute, map[string]string{
		"window_0_name": "5h", "window_0_used_pct": "80.00", "window_0_duration_min": "300",
		"window_0_reset_at": read.Add(72 * time.Hour).Format(time.RFC3339),
	}))
	r := authFakeReader{events: []*eventschema.Envelope{e}}
	for _, tc := range []struct {
		after time.Duration
		stale bool
	}{
		{10 * time.Minute, false},
		{55 * time.Minute, false},
		{56 * time.Minute, true},
		{48 * time.Hour, true},
	} {
		now := read.Add(tc.after)
		got := VendorWindows(context.Background(), r, "zai", now)
		if len(got) != 1 || got[0].Stale != tc.stale || got[0].Current(now) == tc.stale {
			t.Errorf("%s after the reading: %+v, want stale %v", tc.after, got, tc.stale)
		}
		if got[0].Source != "zai" || !got[0].ObservedAt.Equal(read) {
			t.Errorf("the window names its reading: %+v", got[0])
		}
	}
}

// A polled source on an idle account stores the same reading again every
// heartbeat, so its newest reading is never old while it works: the
// windows stay current through hours with no change.
func TestIdleAccountWithAWorkingMeterIsNotStale(t *testing.T) {
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	goodFor := heartbeat.GoodFor(5 * time.Minute)
	var events []*eventschema.Envelope
	for at := start; at.Before(start.Add(6 * time.Hour)); at = at.Add(heartbeat.Every + 5*time.Minute) {
		events = append(events, stamped("claude-usage-meter", at, goodFor, map[string]string{
			"seven_day_used_pct": "31.00", "seven_day_reset_at": start.Add(96 * time.Hour).Format(time.RFC3339),
		}))
	}
	r := authFakeReader{events: events}
	now := start.Add(6 * time.Hour)
	got := VendorWindows(context.Background(), r, eventschema.ProviderAnthropic, now)
	if len(got) != 1 || got[0].Stale || got[0].NotStarted || !got[0].Current(now) || got[0].UsedPct != 31 {
		t.Fatalf("an idle week read by a working meter: %+v", got)
	}
	weekly, _ := Lookup("claude-max")
	weekly.RateLimitWindow = 7 * 24 * time.Hour
	if a := LatestAuthoritativeWindow(context.Background(), r, eventschema.ProviderAnthropic, weekly, now); a == nil || a.UsedPct != 31 {
		t.Errorf("the session budget has no reading of an idle week: %+v", a)
	}
	// The meter stops: the last reading goes stale once it is older than
	// it said it stands for.
	later := now.Add(goodFor + 20*time.Minute)
	got = VendorWindows(context.Background(), r, eventschema.ProviderAnthropic, later)
	if len(got) != 1 || !got[0].Stale {
		t.Errorf("the meter stopped: %+v", got)
	}
}

// The single reading the router and the session budget ask for is none
// once its reader has stopped: a share nobody is reading any more is not
// the share now.
func TestStaleReadingIsNoAuthoritativeWindow(t *testing.T) {
	read := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	fresh, stopped := read.Add(20*time.Minute), read.Add(3*time.Hour)
	ctx := context.Background()
	claude, _ := Lookup("claude-max")
	codex, _ := Lookup("gpt-plus")

	meter := authFakeReader{events: []*eventschema.Envelope{stamped("claude-usage-meter", read, 25*time.Minute, map[string]string{
		"five_hour_used_pct": "70.00", "five_hour_reset_at": read.Add(4 * time.Hour).Format(time.RFC3339),
	})}}
	if a := LatestAuthoritativeWindow(ctx, meter, eventschema.ProviderAnthropic, claude, fresh); a == nil || a.UsedPct != 70 {
		t.Errorf("claude, reader working: %+v", a)
	}
	if a := LatestAuthoritativeWindow(ctx, meter, eventschema.ProviderAnthropic, claude, stopped); a != nil {
		t.Errorf("claude, reader stopped: %+v", a)
	}

	appServer := authFakeReader{events: []*eventschema.Envelope{stamped("codex-app-server", read, 55*time.Minute, map[string]string{
		"primary_used_pct": "60.00", "primary_window_min": "300",
		"primary_resets_at": strconv.FormatInt(read.Add(4*time.Hour).Unix(), 10),
	})}}
	if a := LatestAuthoritativeWindow(ctx, appServer, eventschema.ProviderOpenAI, codex, fresh); a == nil || a.UsedPct != 60 {
		t.Errorf("codex, reader working: %+v", a)
	}
	if a := LatestAuthoritativeWindow(ctx, appServer, eventschema.ProviderOpenAI, codex, stopped); a != nil {
		t.Errorf("codex, reader stopped: %+v", a)
	}
	// Codex's rollouts are read as work happens: hours old is idle.
	rollout := authFakeReader{events: []*eventschema.Envelope{{Source: "codex-jsonl", Timestamp: read, Attributes: map[string]string{
		"primary_used_pct": "60.00", "primary_window_min": "300",
		"primary_resets_at": strconv.FormatInt(read.Add(4*time.Hour).Unix(), 10),
	}}}}
	if a := LatestAuthoritativeWindow(ctx, rollout, eventschema.ProviderOpenAI, codex, stopped); a == nil || a.UsedPct != 60 {
		t.Errorf("codex, idle rollout: %+v", a)
	}

	cursor := authFakeReader{events: []*eventschema.Envelope{stamped("cursor-web", read, 16*time.Minute, map[string]string{
		"used_pct": "40.00", "billing_cycle_end": read.Add(240 * time.Hour).Format(time.RFC3339),
	})}}
	if a := LatestAuthoritativeMonthly(ctx, cursor, eventschema.ProviderCursor, read.Add(10*time.Minute)); a == nil || a.UsedPct != 40 {
		t.Errorf("cursor, reader working: %+v", a)
	}
	if a := LatestAuthoritativeMonthly(ctx, cursor, eventschema.ProviderCursor, stopped); a != nil {
		t.Errorf("cursor, reader stopped: %+v", a)
	}

	copilot := authFakeReader{events: []*eventschema.Envelope{stamped("github-copilot", read, 16*time.Minute, map[string]string{
		"percent_remaining": "30.00", "entitlement": "300", "quota_reset_date": "2026-12-01",
	})}}
	if a := LatestAuthoritativeMonthly(ctx, copilot, eventschema.ProviderGitHub, read.Add(10*time.Minute)); a == nil || a.UsedPct != 70 {
		t.Errorf("copilot, reader working: %+v", a)
	}
	if a := LatestAuthoritativeMonthly(ctx, copilot, eventschema.ProviderGitHub, stopped); a != nil {
		t.Errorf("copilot, reader stopped: %+v", a)
	}
}

// A vendor's spend figure from a reader that has stopped is shown as what
// it is, an old figure, and decides nothing: not the risk, and not that
// the limit is reached.
func TestStaleVendorSpendDecidesNothing(t *testing.T) {
	read := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	e := of(eventschema.ProviderFireworks, stamped("fireworks-usage", read, 55*time.Minute, map[string]string{
		"extra_usage_used": "96.00", "extra_usage_limit": "100.00", "extra_usage_currency": "USD",
		"extra_usage_limit_reached": "true",
	}))
	r := authFakeReader{events: []*eventschema.Envelope{e}}
	ctx := context.Background()
	p := Plan{Name: "fireworks-payg", Provider: "fireworks", Display: "Fireworks"}

	working := read.Add(30 * time.Minute)
	v := LatestVendorSpend(ctx, r, eventschema.ProviderFireworks, working)
	if v == nil || v.Stale {
		t.Fatalf("reader working: %+v", v)
	}
	report := computeSpendHeadroom(p, HeadroomInputs{Now: working, VendorSpend: v})
	if report.OverageRisk != RiskHigh || !report.LimitReached || report.SpendStale {
		t.Errorf("reader working: %+v", report)
	}

	stopped := read.Add(26 * time.Hour)
	v = LatestVendorSpend(ctx, r, eventschema.ProviderFireworks, stopped)
	if v == nil || !v.Stale || v.UsedUSD != 96 {
		t.Fatalf("reader stopped: %+v", v)
	}
	report = computeSpendHeadroom(p, HeadroomInputs{Now: stopped, VendorSpend: v})
	if report.OverageRisk != RiskUnknown || report.LimitReached || report.BlockedUntil != nil {
		t.Errorf("an old figure decided: %+v", report)
	}
	if !report.SpendStale || report.SpendUSD != 96 || report.SpendLimitUSD != 100 || report.SpendPct != 96 || report.SpendSource != "vendor" {
		t.Errorf("the old figure is shown as old: %+v", report)
	}
	if !report.StaleSpend() || report.SpendReadAt == nil || !report.SpendReadAt.Equal(read) || report.SpendReadAgo(stopped) != 26*time.Hour {
		t.Errorf("the report does not say the spend was read at %s: %+v", read, report)
	}
	if want := "the vendor's spend figure is 26h old: its reader has stopped, so what is spent now is unknown"; report.Note != want {
		t.Errorf("note %q, want %q", report.Note, want)
	}
}

// A prepaid balance from a reader that has stopped is shown and decides
// nothing either.
func TestStalePrepaidBalanceDecidesNothing(t *testing.T) {
	read := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	e := of("deepseek", stamped("deepseek", read, 55*time.Minute, map[string]string{
		"extra_usage_limit": "0", "extra_usage_currency": "USD", "billing": "per_token", "balance_usd": "0.00",
	}))
	r := authFakeReader{events: []*eventschema.Envelope{e}}
	p := Plan{Name: "deepseek-payg", Provider: "deepseek", Display: "DeepSeek"}

	working := read.Add(30 * time.Minute)
	report := computeSpendHeadroom(p, HeadroomInputs{Now: working, VendorSpend: LatestVendorSpend(context.Background(), r, "deepseek", working)})
	if report.OverageRisk != RiskHigh || report.SpendStale {
		t.Errorf("reader working, balance used up: %+v", report)
	}
	stopped := read.Add(5 * time.Hour)
	report = computeSpendHeadroom(p, HeadroomInputs{Now: stopped, VendorSpend: LatestVendorSpend(context.Background(), r, "deepseek", stopped)})
	if report.OverageRisk != RiskUnknown || !report.SpendStale || report.BalanceUSD == nil {
		t.Errorf("reader stopped: %+v", report)
	}
	// The balance is old; the spend beside it is the local estimate, which
	// is not.
	if report.StaleSpend() || report.SpendReadAgo(stopped) != 5*time.Hour {
		t.Errorf("the balance is the old figure, not the spend: %+v", report)
	}
	if working := (HeadroomReport{SpendSource: "vendor", SpendReadAt: &read}); working.StaleSpend() || working.SpendReadAgo(stopped) != 0 {
		t.Error("a current figure reads as old")
	}
	// What was spent locally is current, and is still held against the
	// limit the operator set.
	report = computeSpendHeadroom(p, HeadroomInputs{Now: stopped, SpendUSD: 7, SpendLimitUSD: 10, VendorSpend: LatestVendorSpend(context.Background(), r, "deepseek", stopped)})
	if !report.SpendStale || report.SpendPct != 70 || report.OverageRisk != RiskMedium {
		t.Errorf("local spend against the configured limit: %+v", report)
	}
	if want := "the vendor's prepaid balance is 5h old: its reader has stopped, so what is left now is unknown"; report.Note != want {
		t.Errorf("note %q, want %q", report.Note, want)
	}
}
