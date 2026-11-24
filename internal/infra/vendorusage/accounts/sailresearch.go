package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerSailResearch registers the reader (readers_gen.go).
func readerSailResearch() usage.Reader { return SailResearch{} }

// SailResearch reads GET api.sailresearch.com/v2/usage/summary?range=30d,
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/
// sailresearch.ts, Providers/SailResearch, docs/sailresearch.md; Sail's
// docs at docs.sailresearch.com/usage-endpoints): the organisation's net
// credit balance and its spend over the range Sail applied, in fractional
// USD cents. A free organisation can be given seven days for thirty;
// the range Sail reports is the one recorded. A 402 is the key disabled
// for exhausted credits.
type SailResearch struct {
	BaseURL string
	HTTP    *http.Client
}

func (SailResearch) Endpoint() string               { return "sailresearch" }
func (SailResearch) Provider() eventschema.Provider { return "sailresearch" }
func (SailResearch) Source() string                 { return "sailresearch-account" }

// sailRanges are the ranges Sail reports spend over; "period" is the
// billing period.
var sailRanges = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour,
	"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "period": 0,
}

var errSailShape = errors.New("accounts: sail research usage summary: unrecognised answer")

func (s SailResearch) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Object             string   `json:"object"`
		Available          *bool    `json:"available"`
		HasCustomer        *bool    `json:"has_metronome_customer"`
		BalanceUnavailable *bool    `json:"balance_unavailable"`
		Balance            *float64 `json:"balance"`
		PeriodSpend        *float64 `json:"period_spend"`
		EffectiveRange     string   `json:"effective_range"`
	}
	err := getJSON(ctx, s.HTTP, base(s.BaseURL, "https://api.sailresearch.com")+"/v2/usage/summary?range=30d", key, &resp)
	var se *statusError
	if errors.As(err, &se) && se.status == http.StatusPaymentRequired {
		// Sail disabled the key: its credits are exhausted.
		return usage.Reading{Scope: "organization", LimitReached: true}, nil
	}
	if err != nil {
		return usage.Reading{}, err
	}
	switch {
	case resp.Object != "usage.summary" || resp.Available == nil:
		return usage.Reading{}, errSailShape
	case !*resp.Available:
		return usage.Reading{}, errors.New("accounts: sail research: billing data is unavailable")
	case resp.HasCustomer == nil || resp.BalanceUnavailable == nil:
		return usage.Reading{}, errSailShape
	case !*resp.HasCustomer:
		return usage.Reading{}, errors.New("accounts: sail research: the organisation has no billing account")
	}
	period, ok := sailRanges[resp.EffectiveRange]
	if !ok || resp.PeriodSpend == nil || *resp.PeriodSpend < 0 {
		return usage.Reading{}, errSailShape
	}
	r := usage.Reading{Scope: "organization", UsedUSD: *resp.PeriodSpend / 100, HasUsed: true, UsedPeriod: period}
	switch {
	case *resp.BalanceUnavailable:
		r.Notice = "Sail's credit balance was unavailable"
	case resp.Balance == nil:
		return usage.Reading{}, fmt.Errorf("%w (no balance)", errSailShape)
	default:
		// A negative balance is kept.
		r.BalanceUSD, r.HasBalance = *resp.Balance/100, true
	}
	return r, nil
}
