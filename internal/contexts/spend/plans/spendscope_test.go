package plans

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Fireworks' billing summary is the whole account's month, every member
// of an organisation together, against the account's spend limit. Shown
// bare it reads as the operator's own spend, which can be a small part of
// it. The reading keeps whose figure it is, and the report names it; the
// numbers stay the vendor's.
func TestAccountWideVendorSpendIsLabelledAsSuch(t *testing.T) {
	reading := &eventschema.Envelope{
		Type:      eventschema.EventTypePrompt,
		Timestamp: vsNow.Add(-5 * time.Minute),
		Source:    "fireworks-usage",
		Attributes: map[string]string{
			"billing": "per_token", "scope": "account", "account_id": "acme-test",
			"extra_usage_used": "1000.00", "extra_usage_limit": "4000.00",
			"extra_usage_currency": "USD", "extra_usage_limit_reached": "false",
		},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderFireworks},
	}
	v := LatestVendorSpend(context.Background(), staticReader{reading}, eventschema.ProviderFireworks, vsNow)
	if v == nil || v.Scope != "account" || v.AccountID != "acme-test" {
		t.Fatalf("vendor spend = %+v, want the account scope kept", v)
	}
	report, err := ComputeHeadroom(PayAsYouGo, HeadroomInputs{VendorSpend: v, Now: vsNow})
	if err != nil {
		t.Fatal(err)
	}
	if report.SpendUSD != 1000 || report.SpendLimitUSD != 4000 || report.SpendPct != 25 {
		t.Errorf("figures changed: %.2f of %.2f (%.2f%%)", report.SpendUSD, report.SpendLimitUSD, report.SpendPct)
	}
	if report.SpendScope != "account" || report.SpendScopeLabel != "account acme-test, all members" {
		t.Errorf("scope %q label %q", report.SpendScope, report.SpendScopeLabel)
	}
}

// A member's own cap is the operator's figure and carries no label.
func TestOwnVendorSpendCarriesNoAccountLabel(t *testing.T) {
	report, _ := ComputeHeadroom(PayAsYouGo, HeadroomInputs{
		VendorSpend: &VendorSpend{UsedUSD: 10, LimitUSD: 100, Scope: "user", AccountID: "acme-test"},
		Now:         vsNow,
	})
	if report.SpendScopeLabel != "" || report.SpendScope != "user" {
		t.Errorf("scope %q label %q", report.SpendScope, report.SpendScopeLabel)
	}
}

// An account-wide figure that stands in for the operator's own says why,
// so the fallback is never silent; a member blocked by their cap carries
// the block and its end.
func TestVendorSpendCarriesTheReasonAndTheBlock(t *testing.T) {
	reading := func(attrs map[string]string) *VendorSpend {
		base := map[string]string{"billing": "per_token", "account_id": "acme-test",
			"extra_usage_currency": "USD", "extra_usage_limit": "100.00"}
		for k, v := range attrs {
			base[k] = v
		}
		env := &eventschema.Envelope{Type: eventschema.EventTypePrompt, Timestamp: vsNow.Add(-time.Minute),
			Source: "fireworks-usage", Attributes: base,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderFireworks}}
		return LatestVendorSpend(context.Background(), staticReader{env}, eventschema.ProviderFireworks, vsNow)
	}
	v := reading(map[string]string{"scope": "account", "extra_usage_used": "900.00", "scope_reason": "your user wasn't found"})
	report, _ := ComputeHeadroom(PayAsYouGo, HeadroomInputs{VendorSpend: v, Now: vsNow})
	if report.SpendScopeReason != "your user wasn't found" {
		t.Errorf("reason %q", report.SpendScopeReason)
	}
	until := vsNow.Add(72 * time.Hour).UTC().Truncate(time.Second)
	v = reading(map[string]string{"scope": "user", "extra_usage_used": "105.34", "extra_usage_limit_reached": "true",
		"extra_usage_blocked_until": until.Format(time.RFC3339)})
	report, _ = ComputeHeadroom(PayAsYouGo, HeadroomInputs{VendorSpend: v, Now: vsNow})
	if !report.LimitReached || report.BlockedUntil == nil || !report.BlockedUntil.Equal(until) || report.SpendScopeReason != "" {
		t.Errorf("limit reached %v until %v reason %q", report.LimitReached, report.BlockedUntil, report.SpendScopeReason)
	}
	// A block that has ended is not shown.
	report, _ = ComputeHeadroom(PayAsYouGo, HeadroomInputs{VendorSpend: v, Now: until.Add(time.Minute)})
	if report.BlockedUntil != nil {
		t.Errorf("an ended block is still shown: %v", report.BlockedUntil)
	}
}
