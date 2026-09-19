package plans

import (
	"math"
	"testing"
	"time"
)

// Usage-based Enterprise has no rate-limit window: it is billed at API rates
// from the first token, so there is no cap to be under and no percentage to
// report. What it does have is a spend limit the org's admins set in the
// vendor console — a number the operator knows and we never could. So the
// plan is spend-denominated, and the limit is supplied rather than guessed.
func TestEnterpriseIsSpendDenominated(t *testing.T) {
	p, ok := Lookup("claude-enterprise")
	if !ok {
		t.Fatal("claude-enterprise should exist as a spend-denominated plan")
	}
	if !p.SpendDenominated {
		t.Error("Enterprise measures spend against a limit, not messages against a window")
	}
	if p.RateLimitWindow != 0 || p.MessagesPerWindow != 0 {
		t.Errorf("Enterprise must not claim a rate-limit window: %+v", p)
	}
}

// The limit is the operator's to supply. Binding the plan without one must
// fail rather than default to a number nobody chose.
func TestSpendDenominatedPlanNeedsALimit(t *testing.T) {
	if err := ValidateSpendLimit("claude-enterprise", 0, false); err == nil {
		t.Fatal("binding a spend-denominated plan with no limit should be refused")
	}
	if err := ValidateSpendLimit("claude-enterprise", 5000, false); err != nil {
		t.Fatalf("a supplied limit should be accepted: %v", err)
	}
	// A windowed plan neither needs nor accepts one.
	if err := ValidateSpendLimit("claude-max-20x", 0, false); err != nil {
		t.Fatalf("a windowed plan needs no spend limit: %v", err)
	}
}

func TestHeadroomReportsSpendAgainstTheLimit(t *testing.T) {
	p, _ := Lookup("claude-enterprise")
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	got := computeHeadroomFor(p, HeadroomInputs{
		SpendLimitUSD: 5000,
		SpendUSD:      1203.44,
		Now:           now,
	})
	if math.Abs(got.SpendPct-24.07) > 0.05 {
		t.Errorf("SpendPct = %v, want ~24.07", got.SpendPct)
	}
	if got.SpendLimitUSD != 5000 || math.Abs(got.SpendUSD-1203.44) > 0.001 {
		t.Errorf("spend block = %+v", got)
	}
	if got.OverageRisk != RiskLow {
		t.Errorf("OverageRisk = %q, want low at 24%%", got.OverageRisk)
	}
	// A window it does not have must not be invented.
	if got.WindowCap != 0 || got.WindowPct != 0 {
		t.Errorf("Enterprise report must carry no window: %+v", got)
	}
}

func TestSpendRiskEscalates(t *testing.T) {
	p, _ := Lookup("claude-enterprise")
	for _, tc := range []struct {
		spend float64
		want  string
	}{
		{1000, RiskLow},
		{3200, RiskMedium},
		{4500, RiskHigh},
	} {
		got := computeHeadroomFor(p, HeadroomInputs{SpendLimitUSD: 5000, SpendUSD: tc.spend, Now: time.Now()})
		if got.OverageRisk != tc.want {
			t.Errorf("spend %.0f of 5000: risk = %q, want %q (pct=%v)", tc.spend, got.OverageRisk, tc.want, got.SpendPct)
		}
	}
}

// Enterprise contracts are frequently discounted off list, and tokenops
// costs from the public rate card. Applying the factor keeps a console limit
// comparable to what we measured; without it the overstatement is invisible.
func TestRateFactorDiscountsMeasuredSpend(t *testing.T) {
	p, _ := Lookup("claude-enterprise")
	got := computeHeadroomFor(p, HeadroomInputs{
		SpendLimitUSD: 5000,
		SpendUSD:      1000,
		RateFactor:    0.8,
		Now:           time.Now(),
	})
	if math.Abs(got.SpendUSD-800) > 0.001 {
		t.Errorf("SpendUSD = %v, want 800 after a 0.8 rate factor", got.SpendUSD)
	}
}

// A spend-denominated plan with no limit configured reports the spend and
// says why there is no percentage, rather than dividing by zero or showing
// a comfortable 0%.
func TestSpendDenominatedWithoutALimitSaysSo(t *testing.T) {
	p, _ := Lookup("claude-enterprise")
	got := computeHeadroomFor(p, HeadroomInputs{SpendUSD: 400, Now: time.Now()})
	if got.SpendPct != 0 {
		t.Errorf("SpendPct = %v, want 0 with no limit", got.SpendPct)
	}
	if got.OverageRisk != RiskUnknown {
		t.Errorf("OverageRisk = %q, want unknown without a limit", got.OverageRisk)
	}
	if got.Note == "" {
		t.Error("want a note explaining the missing limit")
	}
}

// With the Claude usage meter on, Anthropic reports the limit itself, so
// the plan can be bound without one typed in.
func TestValidateSpendLimitAcceptsAVendorReportedLimit(t *testing.T) {
	if err := ValidateSpendLimit("claude-enterprise", 0, true); err != nil {
		t.Errorf("refused a binding whose limit the vendor reports: %v", err)
	}
}
