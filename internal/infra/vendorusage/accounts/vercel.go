package accounts

import (
	"context"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerVercel registers the reader (readers_gen.go).
func readerVercel() usage.Reader { return Vercel{} }

// Vercel reads the AI Gateway's credit balance from GET /v1/credits
// (https://vercel.com/docs/ai-gateway/sdks-and-apis/rest-api). It is the
// team's balance, not the key's; total_used is lifetime and not read.
type Vercel struct {
	BaseURL string
	HTTP    *http.Client
}

func (Vercel) Endpoint() string               { return "vercel" }
func (Vercel) Provider() eventschema.Provider { return "vercel" }
func (Vercel) Source() string                 { return "vercel-account" }

func (v Vercel) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Balance number `json:"balance"`
	}
	if err := getJSON(ctx, v.HTTP, base(v.BaseURL, "https://ai-gateway.vercel.sh")+"/v1/credits", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if !resp.Balance.ok {
		return usage.Reading{}, errors.New("accounts: Vercel AI Gateway credits: no balance in the answer")
	}
	return usage.Reading{Scope: "team", BalanceUSD: resp.Balance.v, HasBalance: true, LimitReached: resp.Balance.v <= 0}, nil
}
