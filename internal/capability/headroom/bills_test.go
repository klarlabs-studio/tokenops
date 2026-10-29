package headroom

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Only the operator's own spend this calendar month, as the vendor
// reported it, is a bill a budget counts.
func TestOnlyTheOperatorsOwnMonthIsABill(t *testing.T) {
	got := Bills([]plans.HeadroomReport{
		{Provider: "fireworks", SpendSource: "vendor", SpendScope: "user", SpendUSD: 130},
		{Provider: "anthropic", SpendSource: "vendor", SpendUSD: 12},
		{Provider: "openrouter", SpendSource: "estimate", SpendUSD: 7},
		{Provider: "mistral", SpendSource: "vendor", SpendScope: "account", SpendUSD: 900},
		{Provider: "xai", SpendSource: "vendor", SpendUSD: 2, SpendTrailingDays: 30},
	})
	if len(got) != 2 || got["fireworks"] != 130 || got["anthropic"] != 12 {
		t.Errorf("bills = %v; want fireworks' own 130 and anthropic's 12 only", got)
	}
}
