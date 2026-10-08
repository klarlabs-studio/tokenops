package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// gatewaySub2API registers the gateway (gateways_gen.go).
func gatewaySub2API() usage.Gateway { return Sub2API{} }

// Sub2API reads GET /v1/usage on a sub2api deployment (Wei-Shaw/sub2api,
// GatewayHandler.Usage), which a group API key may call about itself, as
// CodexBar's sub2api provider does. The answer depends on the key's group:
//
//   - a quota-limited key: its total quota in USD and optional 5-hour,
//     daily and 7-day spend limits, each with when it resets;
//   - a subscription group: daily, weekly and monthly spend against the
//     group's limits, on the subscription's own billing anchors, which the
//     answer does not give, so those windows carry no reset;
//   - a wallet group: the owner's balance.
type Sub2API struct {
	// HTTP is the client; nil uses a default.
	HTTP *http.Client
}

func (Sub2API) Name() string   { return "sub2api" }
func (Sub2API) Source() string { return "sub2api-account" }

// Recognise asks GET /setup/status, which sub2api answers without a key
// (backend/internal/server/routes/common.go).
func (g Sub2API) Recognise(ctx context.Context, root string) bool {
	body, ok := probe(ctx, g.HTTP, root+"/setup/status")
	if !ok {
		return false
	}
	var s struct {
		Data *struct {
			NeedsSetup *bool   `json:"needs_setup"`
			Step       *string `json:"step"`
		} `json:"data"`
	}
	return json.Unmarshal(body, &s) == nil && s.Data != nil && s.Data.NeedsSetup != nil && s.Data.Step != nil
}

// sub2apiWindows are the rate-limit windows sub2api reports.
var sub2apiWindows = map[string]time.Duration{"5h": 5 * time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

func (g Sub2API) Read(ctx context.Context, root, key string) (usage.Reading, error) {
	var resp struct {
		IsValid *bool  `json:"isValid"`
		Status  string `json:"status"`
		Unit    string `json:"unit"`
		Balance number `json:"balance"`
		Quota   *struct {
			Limit number `json:"limit"`
			Used  number `json:"used"`
			Unit  string `json:"unit"`
		} `json:"quota"`
		Subscription *struct {
			DailyUsage   number `json:"daily_usage_usd"`
			WeeklyUsage  number `json:"weekly_usage_usd"`
			MonthlyUsage number `json:"monthly_usage_usd"`
			DailyLimit   number `json:"daily_limit_usd"`
			WeeklyLimit  number `json:"weekly_limit_usd"`
			MonthlyLimit number `json:"monthly_limit_usd"`
		} `json:"subscription"`
		RateLimits []struct {
			Window  string `json:"window"`
			Limit   number `json:"limit"`
			Used    number `json:"used"`
			ResetAt string `json:"reset_at"`
		} `json:"rate_limits"`
	}
	if err := getGateway(ctx, g.HTTP, root+"/v1/usage", "Authorization", "Bearer "+key, &resp); err != nil {
		return usage.Reading{}, err
	}
	// sub2api answers 200 for a key it no longer accepts.
	if resp.IsValid != nil && !*resp.IsValid {
		return usage.Reading{}, fmt.Errorf("%w (sub2api reports the key invalid)", usage.ErrAuth)
	}
	r := usage.Reading{Scope: "key", LimitReached: resp.Status == "quota_exhausted" || resp.Status == "expired"}
	if s := resp.Subscription; s != nil {
		r.Subscription = true
		for _, w := range []struct {
			used, limit number
			d           time.Duration
		}{
			{s.DailyUsage, s.DailyLimit, 24 * time.Hour},
			{s.WeeklyUsage, s.WeeklyLimit, 7 * 24 * time.Hour},
			{s.MonthlyUsage, s.MonthlyLimit, 30 * 24 * time.Hour},
		} {
			if w.limit.ok && w.limit.v > 0 {
				r.Windows = append(r.Windows, usage.Window{Name: windowName(w.d), UsedPct: pct(w.used.v, w.limit.v), Duration: w.d})
			}
		}
	} else if q := resp.Quota; q != nil && q.Limit.ok && q.Limit.v > 0 {
		if usd(q.Unit, resp.Unit) {
			r.UsedUSD, r.HasUsed, r.LimitUSD = q.Used.v, true, q.Limit.v
			r.LimitReached = r.LimitReached || q.Used.v >= q.Limit.v
		} else {
			r.Windows = append(r.Windows, usage.Window{Name: "quota", UsedPct: pct(q.Used.v, q.Limit.v)})
		}
	}
	for _, l := range resp.RateLimits {
		d, ok := sub2apiWindows[strings.ToLower(l.Window)]
		if !ok || !l.Limit.ok || l.Limit.v <= 0 {
			continue
		}
		r.Windows = append(r.Windows, usage.Window{Name: windowName(d), UsedPct: pct(l.Used.v, l.Limit.v), Duration: d, ResetsAt: parseTime(l.ResetAt)})
	}
	// A wallet's balance is the owner's, in the deployment's unit.
	if resp.Balance.ok && usd(resp.Unit, "") {
		r.BalanceUSD, r.HasBalance = resp.Balance.v, true
	}
	return r, nil
}

// usd reports whether an amount is in US dollars: its unit, or failing
// that the answer's; sub2api's default is USD.
func usd(unit, fallback string) bool {
	if unit == "" {
		unit = fallback
	}
	return unit == "" || strings.EqualFold(unit, "USD")
}
