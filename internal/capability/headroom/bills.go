package headroom

import "go.klarlabs.de/tokenops/internal/contexts/spend/plans"

// Bills is each provider's own figure for the operator's spend this
// calendar month, from reports: the bill a budget on that provider counts
// (budgets.Billed). Only spend the vendor reported, for this calendar
// month, and the operator's own is a bill: an account-wide figure is every
// member's spend together, and a trailing period (xAI's 30 days) is not
// the month a budget counts.
func Bills(reports []plans.HeadroomReport) map[string]float64 {
	out := map[string]float64{}
	for _, r := range reports {
		if r.SpendSource == "vendor" && r.SpendScope != "account" && r.SpendTrailingDays == 0 {
			out[r.Provider] = r.SpendUSD
		}
	}
	return out
}
