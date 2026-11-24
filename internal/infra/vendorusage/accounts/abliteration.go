package accounts

import (
	"context"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerAbliteration registers the reader (readers_gen.go).
func readerAbliteration() usage.Reader { return Abliteration{} }

// Abliteration reads GET api.abliteration.ai/v1/credits, ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/abliteration.ts,
// Providers/Abliteration, docs/abliteration.md): the credits bought and
// used, both USD. The balance is their difference, floored at zero as
// OpenRouter's credits show it. No reset or allowance is inferred.
type Abliteration struct {
	BaseURL string
	HTTP    *http.Client
}

func (Abliteration) Endpoint() string               { return "abliteration" }
func (Abliteration) Provider() eventschema.Provider { return "abliteration" }
func (Abliteration) Source() string                 { return "abliteration-account" }

func (a Abliteration) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Data *struct {
			Total *float64 `json:"total_credits"`
			Usage *float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := getJSON(ctx, a.HTTP, base(a.BaseURL, "https://api.abliteration.ai")+"/v1/credits", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	d := resp.Data
	if d == nil || d.Total == nil || d.Usage == nil || *d.Total < 0 || *d.Usage < 0 {
		return usage.Reading{}, errors.New("accounts: abliteration credits: unrecognised answer")
	}
	return usage.Reading{Scope: "account", BalanceUSD: max(0, *d.Total-*d.Usage), HasBalance: true}, nil
}
