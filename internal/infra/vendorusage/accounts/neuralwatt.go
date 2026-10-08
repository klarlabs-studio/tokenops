package accounts

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerNeuralWatt registers the reader (readers_gen.go).
func readerNeuralWatt() usage.Reader { return NeuralWatt{} }

// NeuralWatt reads GET /v1/quota, as CodexBar's neuralwatt plugin does:
// the prepaid USD credit left (it does not reset; it is topped up), and,
// for a subscriber, the period's kWh allowance used, which resets at the
// period's end. A key whose spending allowance is exhausted is reported
// blocked.
type NeuralWatt struct {
	BaseURL string
	HTTP    *http.Client
}

func (NeuralWatt) Endpoint() string               { return "neuralwatt" }
func (NeuralWatt) Provider() eventschema.Provider { return "neuralwatt" }
func (NeuralWatt) Source() string                 { return "neuralwatt-account" }

func (n NeuralWatt) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Balance *struct {
			Remaining number `json:"credits_remaining_usd"`
			Total     number `json:"total_credits_usd"`
			Used      number `json:"credits_used_usd"`
		} `json:"balance"`
		Subscription *struct {
			BillingInterval string `json:"billing_interval"`
			PeriodStart     string `json:"current_period_start"`
			PeriodEnd       string `json:"current_period_end"`
			Included        number `json:"kwh_included"`
			Used            number `json:"kwh_used"`
			Remaining       number `json:"kwh_remaining"`
		} `json:"subscription"`
		Key struct {
			Allowance *struct {
				Blocked bool `json:"blocked"`
			} `json:"allowance"`
		} `json:"key"`
	}
	if err := getJSON(ctx, n.HTTP, base(n.BaseURL, "https://api.neuralwatt.com")+"/v1/quota", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Balance == nil {
		return usage.Reading{}, errors.New("accounts: neuralwatt quota: no balance")
	}
	r := usage.Reading{Scope: "account", LimitReached: resp.Key.Allowance != nil && resp.Key.Allowance.Blocked}
	b := resp.Balance
	switch {
	case b.Remaining.ok:
		r.BalanceUSD, r.HasBalance = math.Max(0, b.Remaining.v), true
	case b.Total.ok && b.Used.ok:
		r.BalanceUSD, r.HasBalance = math.Max(0, b.Total.v-b.Used.v), true
	}
	if s := resp.Subscription; s != nil {
		included := s.Included.v
		if !s.Included.ok && s.Used.ok && s.Remaining.ok {
			included = s.Used.v + s.Remaining.v
		}
		used, ok := s.Used.v, s.Used.ok
		if !ok && s.Remaining.ok {
			used, ok = math.Max(0, included-s.Remaining.v), true
		}
		if included > 0 && ok {
			end := parseTime(s.PeriodEnd)
			var d time.Duration
			if start := parseTime(s.PeriodStart); !start.IsZero() && end.After(start) {
				d = end.Sub(start).Round(time.Hour)
			}
			r.Subscription = true
			r.Windows = append(r.Windows, usage.Window{Name: periodName(d, s.BillingInterval), UsedPct: math.Min(100, pct(used, included)), Duration: d, ResetsAt: end})
		}
	}
	if !r.HasBalance && len(r.Windows) == 0 {
		return usage.Reading{}, errors.New("accounts: neuralwatt quota: no credit or allowance figures")
	}
	return r, nil
}

// periodName names a billing period by its length, or by the interval the
// vendor names when its start is not given.
func periodName(d time.Duration, interval string) string {
	if d > 0 {
		return windowName(d)
	}
	switch interval {
	case "month", "monthly":
		return "month"
	case "week", "weekly":
		return "week"
	case "day", "daily":
		return "day"
	}
	return "period"
}
