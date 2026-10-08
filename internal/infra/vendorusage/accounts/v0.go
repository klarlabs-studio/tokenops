package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerV0 registers the reader (readers_gen.go).
func readerV0() usage.Reader { return V0{} }

// V0 reads the v0 Platform API's GET /v1/user/billing and GET
// /v1/rate-limits (https://v0.app/docs/api/v1/reference/user/get-billing),
// as CodexBar's v0 provider does: the share of the billing cycle's
// balance (or a legacy account's allowance) used, and the share of the
// request quota used. The balances keep v0's own units, so neither is
// read as dollars. The default scope is read; a project scope is not.
type V0 struct {
	BaseURL string
	HTTP    *http.Client
}

func (V0) Endpoint() string               { return "v0" }
func (V0) Provider() eventschema.Provider { return "v0" }
func (V0) Source() string                 { return "v0-account" }

// v0Quota is a legacy allowance or the rate limit.
type v0Quota struct {
	Limit     number `json:"limit"`
	Remaining number `json:"remaining"`
	Reset     stamp  `json:"reset"`
}

func (q v0Quota) window(name string) (usage.Window, bool) {
	if !q.Limit.ok || q.Limit.v < 0 || !q.Remaining.ok {
		// An unknown remainder has no percentage to report.
		return usage.Window{}, false
	}
	return usage.Window{Name: name, UsedPct: v0Used(q.Limit.v, q.Remaining.v), ResetsAt: q.Reset.t}, true
}

func v0Used(limit, remaining float64) float64 {
	if limit <= 0 {
		return 100
	}
	return clampPct(pct(max(0, limit-remaining), limit))
}

func (v V0) Read(ctx context.Context, key string) (usage.Reading, error) {
	root := base(v.BaseURL, "https://api.v0.dev") + "/v1"
	var billing struct {
		BillingType string          `json:"billingType"`
		Data        json.RawMessage `json:"data"`
	}
	if err := getJSON(ctx, v.HTTP, root+"/user/billing", key, &billing); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account"}
	switch billing.BillingType {
	case "token":
		var t struct {
			Balance *struct {
				Total     number `json:"total"`
				Remaining number `json:"remaining"`
			} `json:"balance"`
			BillingCycle struct {
				End stamp `json:"end"`
			} `json:"billingCycle"`
		}
		if json.Unmarshal(billing.Data, &t) != nil || t.Balance == nil || !t.Balance.Total.ok || !t.Balance.Remaining.ok || t.Balance.Total.v < 0 {
			return usage.Reading{}, errors.New("accounts: GET api.v0.dev/v1/user/billing: unrecognised token billing")
		}
		r.Windows = append(r.Windows, usage.Window{Name: "billing cycle", UsedPct: v0Used(t.Balance.Total.v, t.Balance.Remaining.v), ResetsAt: t.BillingCycle.End.t})
	case "legacy":
		var q v0Quota
		if json.Unmarshal(billing.Data, &q) != nil {
			return usage.Reading{}, errors.New("accounts: GET api.v0.dev/v1/user/billing: unrecognised legacy billing")
		}
		if w, ok := q.window("billing cycle"); ok {
			r.Windows = append(r.Windows, w)
		}
	default:
		return usage.Reading{}, errors.New("accounts: GET api.v0.dev/v1/user/billing: unknown billingType")
	}
	var rate v0Quota
	if err := getJSON(ctx, v.HTTP, root+"/rate-limits", key, &rate); err != nil {
		return usage.Reading{}, err
	}
	if w, ok := rate.window("requests"); ok {
		r.Windows = append(r.Windows, w)
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}
