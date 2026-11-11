package spending

import (
	"context"
	"errors"
	"fmt"
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
	// Last30Unread is set when the 30 days could not be read (in time);
	// Last30 is then empty, not zero usage.
	Last30Unread bool `json:"last_30_days_unread,omitempty"`
}

// ErrLast30Unread is CostOf's error when only the 30 days could not be
// read: the ProviderCost it returns still carries today.
var ErrLast30Unread = errors.New("spending: the last 30 days could not be read")

// CostOf totals provider's usage over the last 24 hours and 30 days; the
// two run at once. When only the 30 days fail (a busy store runs out the
// caller's time on the longer read), today is still returned, marked
// Last30Unread, with an error that wraps ErrLast30Unread: a caller that
// can show a partial figure keeps it, one that cannot treats it as an
// error.
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
	if errToday != nil {
		return ProviderCost{}, errToday
	}
	if err != nil {
		c.Last30Unread = true
		return c, fmt.Errorf("%w: %w", ErrLast30Unread, err)
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

// CostsOf is each provider's usage today and over the last 30 days, the
// providers read at once; a provider whose figures cannot be read is left
// out, and one whose 30 days could not be read keeps today. Empty and
// repeated providers are skipped.
func CostsOf(ctx context.Context, s Summarizer, providers []string, now time.Time) map[string]ProviderCost {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = map[string]ProviderCost{}
	)
	seen := map[string]bool{}
	for _, provider := range providers {
		if seen[provider] || provider == "" {
			continue
		}
		seen[provider] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c, err := CostOf(ctx, s, provider, now); err == nil || errors.Is(err, ErrLast30Unread) {
				mu.Lock()
				out[provider] = c
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}
