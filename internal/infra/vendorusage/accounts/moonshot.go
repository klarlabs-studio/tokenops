package accounts

import (
	"context"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMoonshot registers the reader (readers_gen.go).
func readerMoonshot() usage.Reader { return Moonshot{} }

// Moonshot reads GET /v1/users/me/balance on the international platform
// (https://platform.kimi.ai/docs/api/balance), in USD.
type Moonshot struct {
	BaseURL string
	HTTP    *http.Client
}

func (Moonshot) Endpoint() string               { return "moonshot" }
func (Moonshot) Provider() eventschema.Provider { return "moonshot" }
func (Moonshot) Source() string                 { return "moonshot-account" }

func (m Moonshot) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Data struct {
			AvailableBalance float64 `json:"available_balance"`
		} `json:"data"`
		Status bool `json:"status"`
	}
	if err := getJSON(ctx, m.HTTP, base(m.BaseURL, "https://api.moonshot.ai")+"/v1/users/me/balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	b := resp.Data.AvailableBalance
	return usage.Reading{Scope: "account", BalanceUSD: b, HasBalance: true, LimitReached: b <= 0}, nil
}
