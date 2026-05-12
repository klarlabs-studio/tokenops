package plans

import (
	"math"
	"testing"
	"time"
)

// testPlan exercises the computeHeadroomFor path with a synthetic plan
// carrying a real monthly cap. Most catalog entries publish only rate-
// limit windows, so we drive the math here rather than burdening the
// shipped catalog with fictional quotas.
func testPlan(input, output int64) Plan {
	return Plan{
		Name:                 "test-plan",
		Provider:             "test",
		Display:              "Test Plan",
		InputTokensPerMonth:  input,
		OutputTokensPerMonth: output,
	}
}

// midMonth places "now" at the 15th, giving a stable half-month window
// for headroom math (~15 days remaining) regardless of when the test
// runs in real time.
func midMonth() time.Time {
	return time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
}

func TestHeadroomHealthyUsage(t *testing.T) {
	p := testPlan(8_000_000, 2_000_000) // 10M total
	r := computeHeadroomFor(p, HeadroomInputs{
		ConsumedTokens: 2_000_000, // 20%
		Last7DayTokens: 1_400_000, // 200K/day → ~40 days headroom
		Now:            midMonth(),
	})
	if r.ConsumedPct < 19.9 || r.ConsumedPct > 20.1 {
		t.Errorf("ConsumedPct=%f want ~20", r.ConsumedPct)
	}
	if r.OverageRisk != RiskLow {
		t.Errorf("risk=%q want %q", r.OverageRisk, RiskLow)
	}
}

func TestHeadroomHighRiskNearCap(t *testing.T) {
	p := testPlan(8_000_000, 2_000_000)
	r := computeHeadroomFor(p, HeadroomInputs{
		ConsumedTokens: 8_500_000, // 85%
		Last7DayTokens: 2_100_000, // 300K/day → ~5 days headroom
		Now:            midMonth(),
	})
	if r.OverageRisk != RiskHigh {
		t.Errorf("risk=%q want %q (85%% consumed, ~5d headroom, ~15d in month)", r.OverageRisk, RiskHigh)
	}
}

func TestHeadroomQuotaExhausted(t *testing.T) {
	p := testPlan(8_000_000, 2_000_000)
	r := computeHeadroomFor(p, HeadroomInputs{
		ConsumedTokens: 10_500_000, // over
		Last7DayTokens: 1_000_000,
		Now:            midMonth(),
	})
	if r.HeadroomDays != 0 {
		t.Errorf("HeadroomDays=%f want 0 (exhausted)", r.HeadroomDays)
	}
	if r.OverageRisk != RiskHigh {
		t.Errorf("exhausted risk=%q want %q", r.OverageRisk, RiskHigh)
	}
}

func TestHeadroomInsufficientHistory(t *testing.T) {
	p := testPlan(8_000_000, 2_000_000)
	r := computeHeadroomFor(p, HeadroomInputs{
		ConsumedTokens: 3_000_000,
		Last7DayTokens: 0,
		Now:            midMonth(),
	})
	if r.Note == "" {
		t.Error("expected note about insufficient history")
	}
	if !math.IsNaN(r.HeadroomDays) {
		t.Errorf("HeadroomDays=%f want NaN when history empty", r.HeadroomDays)
	}
}

func TestHeadroomNoPublishedQuota(t *testing.T) {
	// Mirrors most catalog entries (Claude Max, ChatGPT Plus): plans
	// publish rate-limit windows, not monthly caps. Report should
	// note the gap rather than divide by zero.
	p := testPlan(0, 0)
	r := computeHeadroomFor(p, HeadroomInputs{
		ConsumedTokens: 1_000_000,
		Last7DayTokens: 200_000,
		Now:            midMonth(),
	})
	if r.QuotaTokens != 0 {
		t.Errorf("QuotaTokens=%d want 0", r.QuotaTokens)
	}
	if r.Note == "" {
		t.Error("expected note about missing monthly cap")
	}
	if r.OverageRisk != RiskUnknown {
		t.Errorf("risk=%q want %q", r.OverageRisk, RiskUnknown)
	}
}

// windowPlan mirrors a Claude Max 20x shape: rate-limit window + cap,
// no monthly token quota.
func windowPlan(cap int64, window time.Duration) Plan {
	return Plan{
		Name:              "test-window-plan",
		Provider:          "test",
		Display:           "Test Window Plan",
		RateLimitWindow:   window,
		MessagesPerWindow: cap,
		WindowUnit:        "messages",
	}
}

func TestHeadroomWindowOnlyLowUsage(t *testing.T) {
	p := windowPlan(200, 5*time.Hour)
	r := computeHeadroomFor(p, HeadroomInputs{
		WindowMessages: 20, // 10% of cap
		Now:            midMonth(),
	})
	if r.WindowCap != 200 {
		t.Errorf("WindowCap=%d want 200", r.WindowCap)
	}
	if r.WindowConsumed != 20 {
		t.Errorf("WindowConsumed=%d want 20", r.WindowConsumed)
	}
	if r.WindowPct < 9.9 || r.WindowPct > 10.1 {
		t.Errorf("WindowPct=%f want ~10", r.WindowPct)
	}
	if r.OverageRisk != RiskLow {
		t.Errorf("risk=%q want %q at 10%% window usage", r.OverageRisk, RiskLow)
	}
}

func TestHeadroomWindowHighRisk(t *testing.T) {
	p := windowPlan(200, 5*time.Hour)
	r := computeHeadroomFor(p, HeadroomInputs{
		WindowMessages: 170, // 85% of cap
		Now:            midMonth(),
	})
	if r.OverageRisk != RiskHigh {
		t.Errorf("risk=%q want %q at 85%% window usage", r.OverageRisk, RiskHigh)
	}
	if r.WindowResetsIn != "5h0m0s" {
		t.Errorf("WindowResetsIn=%q want 5h0m0s", r.WindowResetsIn)
	}
}

func TestHeadroomMonthlyAndWindowTakesWorse(t *testing.T) {
	// Monthly: 30% — low. Window: 85% — high. Report should surface
	// the high signal so the headline is honest.
	p := testPlan(8_000_000, 2_000_000)
	p.RateLimitWindow = 5 * time.Hour
	p.MessagesPerWindow = 200
	p.WindowUnit = "messages"

	r := computeHeadroomFor(p, HeadroomInputs{
		ConsumedTokens: 3_000_000,
		Last7DayTokens: 700_000,
		WindowMessages: 170,
		Now:            midMonth(),
	})
	if r.OverageRisk != RiskHigh {
		t.Errorf("risk=%q want %q (window dominates)", r.OverageRisk, RiskHigh)
	}
}

func TestComputeHeadroomRejectsUnknownPlan(t *testing.T) {
	_, err := ComputeHeadroom("nonexistent", HeadroomInputs{Now: midMonth()})
	if err == nil {
		t.Fatal("expected error for unknown plan")
	}
}

func TestComputeHeadroomUsesCatalog(t *testing.T) {
	// Smoke test that the catalog path is wired — most plans have no
	// monthly cap, so we just confirm the lookup succeeds and emits a
	// note rather than crashing.
	r, err := ComputeHeadroom("claude-max-20x", HeadroomInputs{
		ConsumedTokens: 0,
		Last7DayTokens: 0,
		Now:            midMonth(),
	})
	if err != nil {
		t.Fatalf("ComputeHeadroom: %v", err)
	}
	if r.Display == "" {
		t.Error("Display should come from catalog")
	}
}
