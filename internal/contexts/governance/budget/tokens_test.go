package budget

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
)

// A flat-rate subscriber's real spend is always ~0, so a dollar limit
// can never trip. Tokens are the quantity they actually consume, and a
// budget must be expressible in them.
func TestTokenBasisTripsOnTokenCount(t *testing.T) {
	l := Limit{
		Name:        "weekly-tokens",
		Window:      WindowWeekly,
		LimitTokens: 2_000_000_000,
		Basis:       BasisTokens,
	}
	alerts := Evaluate(l, 1_950_000_000, nil)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	if alerts[0].Severity != SeverityCrit {
		t.Errorf("severity = %v, want crit (97.5%% of limit)", alerts[0].Severity)
	}
	if strings.Contains(alerts[0].Message, "$") {
		t.Errorf("token budget message must not be denominated in dollars: %q", alerts[0].Message)
	}
	if !strings.Contains(alerts[0].Message, "tokens") {
		t.Errorf("token budget message should name the unit: %q", alerts[0].Message)
	}
}

// A token limit under its warn threshold stays quiet.
func TestTokenBasisQuietBelowWarn(t *testing.T) {
	l := Limit{Name: "b", Window: WindowWeekly, LimitTokens: 1_000_000, Basis: BasisTokens}
	if got := Evaluate(l, 100_000, nil); len(got) != 0 {
		t.Errorf("alerts = %d, want 0 at 10%% of limit", len(got))
	}
}

// A token budget with no LimitTokens set is inert, exactly as a dollar
// budget with no LimitUSD is.
func TestTokenBasisWithoutLimitIsInert(t *testing.T) {
	l := Limit{Name: "b", Window: WindowWeekly, Basis: BasisTokens}
	if got := Evaluate(l, 999_999_999, nil); len(got) != 0 {
		t.Errorf("alerts = %d, want 0 when no token limit is configured", len(got))
	}
}

// Forecast breach works in token space too.
func TestTokenBasisForecastBreach(t *testing.T) {
	l := Limit{Name: "b", Window: WindowWeekly, LimitTokens: 1_000_000, Basis: BasisTokens}
	at := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	fc := []forecast.Prediction{
		{At: at, Value: 300_000},
		{At: at.Add(24 * time.Hour), Value: 300_000},
	}
	alerts := Evaluate(l, 500_000, fc)
	var found bool
	for _, a := range alerts {
		if a.Kind == AlertForecastBreach {
			found = true
			if strings.Contains(a.Message, "$") {
				t.Errorf("token forecast message must not use dollars: %q", a.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected a forecast breach alert, got %+v", alerts)
	}
}

// Dollar bases keep their existing behaviour and formatting.
func TestSpendBasisUnchanged(t *testing.T) {
	l := Limit{Name: "b", Window: WindowMonthly, LimitUSD: 100, Basis: BasisSpend}
	alerts := Evaluate(l, 96, nil)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	if !strings.Contains(alerts[0].Message, "$96.00 of $100.00") {
		t.Errorf("dollar formatting regressed: %q", alerts[0].Message)
	}
}
