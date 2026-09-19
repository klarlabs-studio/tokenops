package plans

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func meterReading(at time.Time, used, limit, currency string, reached bool) *eventschema.Envelope {
	r := "false"
	if reached {
		r = "true"
	}
	return &eventschema.Envelope{
		Type:      eventschema.EventTypePrompt,
		Timestamp: at,
		Source:    "claude-usage-meter",
		Attributes: map[string]string{
			"extra_usage_used": used, "extra_usage_limit": limit,
			"extra_usage_currency": currency, "extra_usage_limit_reached": r,
		},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
	}
}

var vsNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func TestLatestVendorSpendReadsTheNewestReadingThisMonth(t *testing.T) {
	r := staticReader{
		meterReading(vsNow.Add(-3*time.Hour), "1000.00", "1500.00", "USD", false),
		meterReading(vsNow.Add(-time.Hour), "1095.63", "1500.00", "USD", false),
	}
	v := LatestVendorSpend(context.Background(), r, eventschema.ProviderAnthropic, vsNow)
	if v == nil || v.UsedUSD != 1095.63 || v.LimitUSD != 1500 {
		t.Fatalf("vendor spend = %+v, want the newest reading, 1095.63 of 1500", v)
	}
}

// Last month's spend was against a limit that has since reset.
func TestLatestVendorSpendIgnoresLastMonth(t *testing.T) {
	r := staticReader{meterReading(time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC), "1400.00", "1500.00", "USD", false)}
	if v := LatestVendorSpend(context.Background(), r, eventschema.ProviderAnthropic, vsNow); v != nil {
		t.Errorf("used a reading from last month: %+v", v)
	}
}

func TestLatestVendorSpendIgnoresOtherCurrencies(t *testing.T) {
	r := staticReader{meterReading(vsNow.Add(-time.Hour), "900.00", "1500.00", "EUR", false)}
	if v := LatestVendorSpend(context.Background(), r, eventschema.ProviderAnthropic, vsNow); v != nil {
		t.Errorf("read a EUR amount as USD: %+v", v)
	}
}

// The vendor's spend and limit replace the recomputed spend and the
// operator's typed limit: what the vendor bills outranks a recomputation
// from a public rate card (ADR 0003), and a rate factor would discount a
// figure that is already the billed rate.
func TestSpendHeadroomPrefersTheVendorFigures(t *testing.T) {
	report, err := ComputeHeadroom("claude-enterprise", HeadroomInputs{
		SpendUSD:      400,
		SpendLimitUSD: 5000,
		RateFactor:    0.8,
		VendorSpend:   &VendorSpend{UsedUSD: 1095.63, LimitUSD: 1500},
		Now:           vsNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SpendUSD != 1095.63 || report.SpendLimitUSD != 1500 || report.SpendPct != 73.04 {
		t.Errorf("spend %.2f of %.2f (%.2f%%), want the vendor's 1095.63 of 1500 (73.04%%)",
			report.SpendUSD, report.SpendLimitUSD, report.SpendPct)
	}
	if report.SpendSource != "vendor" {
		t.Errorf("spend_source = %q, want vendor", report.SpendSource)
	}
}

// The vendor's own "limit reached" wins even where the arithmetic would
// rate the spend low: it is the vendor that stops the account.
func TestSpendHeadroomTrustsTheVendorOnAReachedLimit(t *testing.T) {
	report, _ := ComputeHeadroom("claude-enterprise", HeadroomInputs{
		VendorSpend: &VendorSpend{UsedUSD: 300, LimitUSD: 1500, LimitReached: true},
		Now:         vsNow,
	})
	if report.OverageRisk != RiskHigh {
		t.Errorf("risk = %s with the vendor reporting the limit reached, want high", report.OverageRisk)
	}
}

// Without a meter reading the configured limit still works, and says so.
func TestSpendHeadroomFallsBackToTheConfiguredLimit(t *testing.T) {
	report, _ := ComputeHeadroom("claude-enterprise", HeadroomInputs{SpendUSD: 500, SpendLimitUSD: 5000, Now: vsNow})
	if report.SpendPct != 10 || report.SpendSource != "estimate" {
		t.Errorf("got %.2f%% from %q, want 10%% from estimate", report.SpendPct, report.SpendSource)
	}
}

// The terminal and the MCP tool both assemble through this, so what each
// used to drop is pinned here: the vendor window % (the terminal never read
// it) and the Enterprise spend limit and vendor spend (MCP never read them).
func TestAssembleHeadroomInputsCarriesEverySource(t *testing.T) {
	ctx := context.Background()
	window := &eventschema.Envelope{
		Type:       eventschema.EventTypePrompt,
		Timestamp:  vsNow.Add(-10 * time.Minute),
		Source:     "claude-usage-meter",
		Attributes: map[string]string{"five_hour_used_pct": "42.00"},
		Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
	}
	spend := meterReading(vsNow.Add(-5*time.Minute), "1095.63", "1500.00", "USD", false)
	r := staticReader{window, spend}

	max, err := AssembleHeadroomInputs(ctx, r, nil, "anthropic", "claude-max-20x", SpendLimit{}, vsNow)
	if err != nil {
		t.Fatal(err)
	}
	if max.Authoritative == nil || max.Authoritative.UsedPct != 42 {
		t.Errorf("windowed plan lost the vendor window reading: %+v", max.Authoritative)
	}

	ent, err := AssembleHeadroomInputs(ctx, r, nil, "anthropic", "claude-enterprise", SpendLimit{LimitUSD: 5000, RateFactor: 0.8}, vsNow)
	if err != nil {
		t.Fatal(err)
	}
	if ent.SpendLimitUSD != 5000 || ent.RateFactor != 0.8 {
		t.Errorf("configured limit not carried: limit=%.0f factor=%.2f", ent.SpendLimitUSD, ent.RateFactor)
	}
	if ent.VendorSpend == nil || ent.VendorSpend.UsedUSD != 1095.63 {
		t.Errorf("vendor spend not carried: %+v", ent.VendorSpend)
	}
}

// claude.ai reports reset times with microseconds and a +00:00 offset
// (observed on a real Max account). A reset that failed to parse would
// fall back to the plan's nominal window length.
func TestParseResetsInReadsClaudeResetTimes(t *testing.T) {
	now := time.Date(2026, 9, 19, 17, 0, 0, 0, time.UTC)
	got := parseResetsIn("2026-09-19T20:00:00.048431+00:00", now)
	want := 3*time.Hour + 48431*time.Microsecond
	if got != want {
		t.Errorf("resets in %s, want %s", got, want)
	}
}
