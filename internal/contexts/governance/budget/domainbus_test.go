package budget

import (
	"encoding/json"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Only a budget whose spend reached its limit is exceeded. The 75%
// warning and a projected breach used to raise budget.exceeded too, so
// the audit log recorded budgets as exceeded that were not.
func TestExceededEventOnlyForAReachedLimit(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	usd := Limit{Name: "weekly", Window: WindowWeekly, LimitUSD: 100, Basis: BasisSpend}
	cases := map[string]struct {
		alert Alert
		want  bool
	}{
		"75% warning":     {Alert{Kind: AlertThresholdReached, Limit: usd, ActualUSD: 75, Fraction: 0.75}, false},
		"96% critical":    {Alert{Kind: AlertThresholdReached, Limit: usd, ActualUSD: 96, Fraction: 0.96}, false},
		"forecast breach": {Alert{Kind: AlertForecastBreach, Limit: usd, ActualUSD: 60, ProjectedUSD: 140, Fraction: 0.6}, false},
		"limit reached":   {Alert{Kind: AlertThresholdReached, Limit: usd, ActualUSD: 100, Fraction: 1}, true},
		"no limit at all": {Alert{Kind: AlertThresholdReached, Limit: Limit{Name: "x"}, ActualUSD: 5, Fraction: 1}, false},
	}
	for name, tc := range cases {
		if _, got := ExceededEvent(tc.alert, at); got != tc.want {
			t.Errorf("%s: event %v, want %v", name, got, tc.want)
		}
	}
}

// A token budget is exceeded in tokens. Its LimitUSD is zero, so it
// raised no event at all.
func TestExceededEventForATokenBudget(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	l := Limit{Name: "tokens", Window: WindowDaily, LimitTokens: 1_000_000, Basis: BasisTokens}
	env, ok := ExceededEvent(Alert{Kind: AlertThresholdReached, Limit: l, ActualUSD: 1_200_000, Fraction: 1.2}, at)
	if !ok {
		t.Fatal("no event for an exceeded token budget")
	}
	var e Exceeded
	if err := json.Unmarshal(env.Payload.(*eventschema.DomainEvent).Data, &e); err != nil {
		t.Fatal(err)
	}
	if e.Basis != BasisTokens || e.SpentTokens != 1_200_000 || e.LimitTokens != 1_000_000 || e.SpentUSD != 0 {
		t.Errorf("payload %+v", e)
	}
}
