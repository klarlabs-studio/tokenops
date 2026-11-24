package accounts

import (
	"context"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerTavily registers the reader (readers_gen.go).
func readerTavily() usage.Reader { return Tavily{} }

// Tavily reads GET api.tavily.com/usage (docs.tavily.com, "Usage"),
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/tavily.js,
// Providers/Tavily, docs/tavily.md): the account plan's credits used
// against its monthly allowance, the "month" window. Tavily resets the
// credits on the first of the month without saying in which time zone,
// so no reset time is claimed. Credit counts come from Tavily, already
// weighted by what each call costs.
//
// The endpoint allows ten requests per ten minutes for all of an
// account's keys together, so it is read at most every two minutes.
type Tavily struct {
	BaseURL string
	HTTP    *http.Client
}

func (Tavily) Endpoint() string               { return "tavily" }
func (Tavily) Provider() eventschema.Provider { return "tavily" }
func (Tavily) Source() string                 { return "tavily-account" }
func (Tavily) MinInterval() time.Duration     { return 2 * time.Minute }

var errTavilyShape = errors.New("accounts: tavily usage: unrecognised answer")

func (t Tavily) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Key *struct {
			Usage *int64 `json:"usage"`
			Limit *int64 `json:"limit"`
		} `json:"key"`
		Account *struct {
			PlanUsage *int64 `json:"plan_usage"`
			PlanLimit *int64 `json:"plan_limit"`
		} `json:"account"`
	}
	if err := getJSON(ctx, t.HTTP, base(t.BaseURL, "https://api.tavily.com")+"/usage", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	a, k := resp.Account, resp.Key
	if a == nil || k == nil || a.PlanUsage == nil || a.PlanLimit == nil || *a.PlanUsage < 0 || *a.PlanLimit < 0 ||
		k.Usage == nil || *k.Usage < 0 || (k.Limit != nil && *k.Limit < 0) {
		return usage.Reading{}, errTavilyShape
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if *a.PlanLimit > 0 {
		r.Windows = []usage.Window{{Name: "month", UsedPct: clampPct(pct(float64(*a.PlanUsage), float64(*a.PlanLimit)))}}
	}
	// A key with its own credit limit that is spent is blocked.
	r.LimitReached = k.Limit != nil && *k.Limit > 0 && *k.Usage >= *k.Limit
	return r, nil
}
