package spending

import (
	"context"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// Summarizer totals stored events in a window.
type Summarizer interface {
	Summarize(ctx context.Context, f analytics.Filter) (analytics.Summary, error)
}

// Usage is what one provider used in a window.
type Usage struct {
	Tokens int64 `json:"tokens"`
	// CostUSD is what was billed; APIEquivalentUSD what the same usage
	// costs at API list prices, its value where a plan covers it.
	CostUSD          float64 `json:"cost_usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	Requests         int64   `json:"requests"`
	// UnpricedRequests use models with no list price yet; the money
	// leaves them out.
	UnpricedRequests int64 `json:"unpriced_requests"`
}

// UnpricedShare is the fraction of requests the money leaves out.
func (u Usage) UnpricedShare() float64 {
	if u.Requests == 0 {
		return 0
	}
	return float64(u.UnpricedRequests) / float64(u.Requests)
}

// Covered reports whether a plan covers the usage: nothing billed, yet
// it has a value at list prices.
func (u Usage) Covered() bool { return u.CostUSD == 0 && u.APIEquivalentUSD > 0 }

// ProviderCost is a provider's usage over the last day and the last 30.
type ProviderCost struct {
	Today  Usage `json:"today"`
	Last30 Usage `json:"last_30_days"`
}

// CostOf totals provider's usage over the last 24 hours and 30 days; the
// two run at once.
func CostOf(ctx context.Context, s Summarizer, provider string, now time.Time) (ProviderCost, error) {
	var (
		c        ProviderCost
		errToday error
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Today, errToday = usageSince(ctx, s, provider, now.Add(-24*time.Hour))
	}()
	last30, err := usageSince(ctx, s, provider, now.Add(-30*24*time.Hour))
	wg.Wait()
	if err != nil {
		return ProviderCost{}, err
	}
	if errToday != nil {
		return ProviderCost{}, errToday
	}
	c.Last30 = last30
	return c, nil
}

func usageSince(ctx context.Context, s Summarizer, provider string, since time.Time) (Usage, error) {
	sum, err := s.Summarize(ctx, analytics.Filter{Provider: provider, Since: since})
	if err != nil {
		return Usage{}, err
	}
	u := Usage{Tokens: sum.TotalTokens, CostUSD: sum.CostUSD, APIEquivalentUSD: sum.APIEquivalentUSD, Requests: sum.Requests}
	for _, m := range sum.Unpriced {
		u.UnpricedRequests += m.Requests
	}
	return u, nil
}
