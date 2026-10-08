package accounts

import (
	"context"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerDeepInfra registers the reader (readers_gen.go).
func readerDeepInfra() usage.Reader { return DeepInfra{} }

// DeepInfra reads GET /payment/checklist, in DeepInfra's OpenAPI spec:
// spend since the last invoice (recent, USD), the account's limit, and
// whether it is suspended. A negative stripe_balance is prepaid credit.
type DeepInfra struct {
	BaseURL string
	HTTP    *http.Client
}

func (DeepInfra) Endpoint() string               { return "deepinfra" }
func (DeepInfra) Provider() eventschema.Provider { return "deepinfra" }
func (DeepInfra) Source() string                 { return "deepinfra-account" }

func (d DeepInfra) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		StripeBalance number `json:"stripe_balance"`
		Recent        number `json:"recent"`
		Limit         number `json:"limit"`
		Suspended     bool   `json:"suspended"`
	}
	if err := getJSON(ctx, d.HTTP, base(d.BaseURL, "https://api.deepinfra.com")+"/payment/checklist?compute_owed=true", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account", LimitReached: resp.Suspended}
	if resp.Recent.ok {
		r.UsedUSD, r.HasUsed = resp.Recent.v, true
	}
	if resp.Limit.ok && resp.Limit.v > 0 {
		r.LimitUSD = resp.Limit.v
	}
	if resp.StripeBalance.ok && resp.StripeBalance.v < 0 {
		r.BalanceUSD, r.HasBalance = -resp.StripeBalance.v, true
	}
	return r, nil
}
