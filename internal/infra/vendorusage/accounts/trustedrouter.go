package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerTrustedRouter registers the reader (readers_gen.go).
func readerTrustedRouter() usage.Reader { return TrustedRouter{} }

// TrustedRouter reads GET api.trustedrouter.com/v1/key, the OpenRouter
// /key shape with integer microdollar copies of each amount, ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/trustedrouter.ts,
// Providers/TrustedRouter, docs/trustedrouter.md). Only the microdollar
// fields are read; the float dollars are display copies.
//
// A key with a spending limit is spent against it: the spend is the limit
// less limit_remaining, which already excludes requests in flight, and no
// reset is inferred from limit_reset. A key without one reports this UTC
// month's spend. The account's credit needs a management key and is not
// read.
type TrustedRouter struct {
	BaseURL string
	HTTP    *http.Client
}

func (TrustedRouter) Endpoint() string               { return "trustedrouter" }
func (TrustedRouter) Provider() eventschema.Provider { return "trustedrouter" }
func (TrustedRouter) Source() string                 { return "trustedrouter-account" }

var errTrustedRouterShape = errors.New("accounts: trustedrouter key: unrecognised answer")

func (t TrustedRouter) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, t.HTTP, base(t.BaseURL, "https://api.trustedrouter.com")+"/v1/key", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Data == nil {
		return usage.Reading{}, errTrustedRouterShape
	}
	monthly, ok := microdollars(resp.Data["usage_monthly_microdollars"])
	if !ok {
		return usage.Reading{}, errTrustedRouterShape
	}
	for _, f := range []string{"usage_microdollars", "usage_daily_microdollars", "usage_weekly_microdollars"} {
		if _, ok := microdollars(resp.Data[f]); !ok {
			return usage.Reading{}, errTrustedRouterShape
		}
	}
	limitRaw, remainingRaw := resp.Data["limit_microdollars"], resp.Data["limit_remaining_microdollars"]
	if limitRaw == nil || remainingRaw == nil {
		return usage.Reading{}, errTrustedRouterShape // missing, not null
	}
	limitNull, remainingNull := string(limitRaw) == "null", string(remainingRaw) == "null"
	if limitNull != remainingNull {
		return usage.Reading{}, errTrustedRouterShape
	}
	r := usage.Reading{Scope: "key"}
	if limitNull {
		r.UsedUSD, r.HasUsed = monthly, true
		return r, nil
	}
	limit, ok1 := microdollars(limitRaw)
	remaining, ok2 := microdollars(remainingRaw)
	if !ok1 || !ok2 {
		return usage.Reading{}, errTrustedRouterShape
	}
	r.UsedUSD, r.HasUsed = max(0, limit-remaining), true
	r.LimitUSD = limit
	r.LimitReached = limit > 0 && remaining <= 0
	return r, nil
}

// microdollars reads a non-negative integer of microdollars into dollars.
func microdollars(raw json.RawMessage) (float64, bool) {
	var v int64
	if raw == nil || json.Unmarshal(raw, &v) != nil || v < 0 {
		return 0, false
	}
	return float64(v) / 1e6, true
}
