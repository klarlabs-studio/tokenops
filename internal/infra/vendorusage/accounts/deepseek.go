package accounts

import (
	"context"
	"net/http"
	"strconv"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerDeepSeek registers the reader (readers_gen.go).
func readerDeepSeek() usage.Reader { return DeepSeek{} }

// DeepSeek reads GET /user/balance: the prepaid balance
// (https://api-docs.deepseek.com/api/get-user-balance). Only a USD
// balance is used; headroom is in dollars.
type DeepSeek struct {
	BaseURL string
	HTTP    *http.Client
}

func (DeepSeek) Endpoint() string               { return "deepseek" }
func (DeepSeek) Provider() eventschema.Provider { return eventschema.ProviderDeepSeek }
func (DeepSeek) Source() string                 { return "deepseek-account" }

func (d DeepSeek) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := getJSON(ctx, d.HTTP, base(d.BaseURL, "https://api.deepseek.com")+"/user/balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account", LimitReached: !resp.IsAvailable}
	for _, b := range resp.BalanceInfos {
		if b.Currency != "USD" {
			continue
		}
		if v, err := strconv.ParseFloat(b.TotalBalance, 64); err == nil {
			r.BalanceUSD, r.HasBalance = v, true
		}
	}
	return r, nil
}
