package plans

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func copilotEnv(ts time.Time, attrs map[string]string) *eventschema.Envelope {
	e := env(ts, attrs)
	e.Source = "github-copilot"
	e.Payload = &eventschema.PromptEvent{Provider: eventschema.ProviderGitHub}
	return e
}

// One poll stores a snapshot per bucket at the same time. The monthly
// reading took whichever came first, so an unlimited chat bucket at 100%
// remaining could stand for a nearly spent premium allowance: 0% used,
// low risk.
func TestCopilotMonthlyIgnoresBucketsWithoutAnAllowance(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	poll := now.Add(-time.Minute)
	for name, order := range map[string][]map[string]string{
		"chat first": {
			{"snapshot": "chat", "unlimited": "true", "entitlement": "0", "percent_remaining": "100.00"},
			{"snapshot": "premium_interactions", "unlimited": "false", "entitlement": "7000", "percent_remaining": "25.00"},
		},
		"chat last": {
			{"snapshot": "premium_interactions", "unlimited": "false", "entitlement": "7000", "percent_remaining": "25.00"},
			{"snapshot": "chat", "unlimited": "true", "entitlement": "0", "percent_remaining": "100.00"},
		},
	} {
		var events []*eventschema.Envelope
		for _, a := range order {
			events = append(events, copilotEnv(poll, a))
		}
		a := LatestAuthoritativeMonthly(context.Background(), authFakeReader{events: events}, eventschema.ProviderGitHub, now)
		if a == nil || a.UsedPct != 75 {
			t.Errorf("%s: monthly = %+v; want premium_interactions at 75%% used", name, a)
		}
	}
}

// Copilot Free meters chat and completions too: the busiest bucket of the
// newest poll is the one that stops work. An older poll does not count.
func TestCopilotMonthlyTakesTheBusiestBucketOfTheNewestPoll(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	events := []*eventschema.Envelope{
		copilotEnv(now.Add(-time.Hour), map[string]string{"snapshot": "chat", "entitlement": "50", "percent_remaining": "1.00"}),
		copilotEnv(now.Add(-time.Minute), map[string]string{"snapshot": "chat", "entitlement": "50", "percent_remaining": "60.00"}),
		copilotEnv(now.Add(-time.Minute), map[string]string{"snapshot": "completions", "entitlement": "2000", "percent_remaining": "30.00"}),
	}
	a := LatestAuthoritativeMonthly(context.Background(), authFakeReader{events: events}, eventschema.ProviderGitHub, now)
	if a == nil || a.UsedPct != 70 {
		t.Errorf("monthly = %+v; want completions at 70%% used", a)
	}
}

// An org seat reports every bucket unlimited with no allowance: there is
// no monthly share, and the seat is reported as unmetered with the credits
// it used.
func TestAnUnmeteredCopilotSeatIsNotUnlimitedHeadroom(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	poll := now.Add(-time.Minute)
	events := []*eventschema.Envelope{
		copilotEnv(poll, map[string]string{"snapshot": "premium_interactions", "unlimited": "true", "entitlement": "0", "percent_remaining": "100.00"}),
		copilotEnv(poll, map[string]string{"snapshot": "plan", "seat": "unmetered", "used_credits": "2111", "used_credits_unit": "AI credits"}),
	}
	reader := authFakeReader{events: events}
	if a := LatestAuthoritativeMonthly(context.Background(), reader, eventschema.ProviderGitHub, now); a != nil {
		t.Errorf("monthly = %+v; an unmetered seat has no share used", a)
	}
	seat := LatestUnmeteredSeat(context.Background(), reader, eventschema.ProviderGitHub, now)
	if seat == nil || !seat.HasCredits || seat.CreditsUsed != 2111 {
		t.Fatalf("seat = %+v; want an unmetered seat with 2111 credits used", seat)
	}
	r := computeHeadroomFor(catalog["copilot-business"], HeadroomInputs{UnmeteredSeat: seat, Now: now})
	if r.OverageRisk != RiskUnknown {
		t.Errorf("risk = %s; an unmetered seat's risk is unknown, not low", r.OverageRisk)
	}
	for _, want := range []string{"no personal allowance", "organisation", "2111 AI credits"} {
		if !strings.Contains(r.Note, want) {
			t.Errorf("note %q lacks %q", r.Note, want)
		}
	}

	quiet := LatestUnmeteredSeat(context.Background(), authFakeReader{events: []*eventschema.Envelope{
		copilotEnv(poll, map[string]string{"snapshot": "plan", "seat": "unmetered"}),
	}}, eventschema.ProviderGitHub, now)
	r = computeHeadroomFor(catalog["copilot-business"], HeadroomInputs{UnmeteredSeat: quiet, Now: now})
	if !strings.Contains(r.Note, "no usage reported") {
		t.Errorf("note %q; want it to say no usage was reported", r.Note)
	}
}

// A seat that became metered after an unmetered reading is metered.
func TestTheNewestCopilotReadingDecidesTheSeat(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	events := []*eventschema.Envelope{
		copilotEnv(now.Add(-time.Hour), map[string]string{"snapshot": "plan", "seat": "unmetered"}),
		copilotEnv(now.Add(-time.Minute), map[string]string{"snapshot": "plan", "seat": "metered"}),
	}
	if seat := LatestUnmeteredSeat(context.Background(), authFakeReader{events: events}, eventschema.ProviderGitHub, now); seat != nil {
		t.Errorf("seat = %+v; the newest reading is metered", seat)
	}
}

// Copilot's month of AI Credits is a vendor window like any other.
func TestCopilotCreditsAreAVendorWindow(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	events := []*eventschema.Envelope{copilotEnv(now.Add(-time.Minute), map[string]string{
		"snapshot": "plan", "seat": "metered",
		"window_0_name": "month (AI credits)", "window_0_used_pct": "75.00", "window_0_reset_at": "2026-11-01T00:00:00Z",
	})}
	ws := VendorWindows(context.Background(), authFakeReader{events: events}, eventschema.ProviderGitHub, now)
	if len(ws) != 1 || ws[0].Name != "month (AI credits)" || ws[0].UsedPct != 75 || ws[0].ResetsAt.IsZero() {
		t.Errorf("windows = %+v; want month (AI credits) at 75%%", ws)
	}
}
