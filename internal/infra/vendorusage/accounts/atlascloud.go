package accounts

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerAtlasCloud registers the reader (readers_gen.go).
func readerAtlasCloud() usage.Reader { return AtlasCloud{} }

// AtlasCloud reads GET /public/v1/balance: the account-wide available USD
// balance (https://www.atlascloud.ai/docs/public-api/balance). The key
// needs balance read permission (the owner's key, or an Account Admin or
// Finance key for a team). Coding Plan quotas are a separate meter and
// are not in this answer.
type AtlasCloud struct {
	BaseURL string
	HTTP    *http.Client
}

func (AtlasCloud) Endpoint() string               { return "atlascloud" }
func (AtlasCloud) Provider() eventschema.Provider { return "atlascloud" }
func (AtlasCloud) Source() string                 { return "atlascloud-account" }

func (a AtlasCloud) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Object    string `json:"object"`
		Scope     string `json:"scope"`
		Available struct {
			Value    string `json:"value"`
			Currency string `json:"currency"`
		} `json:"available"`
	}
	url := base(a.BaseURL, "https://api.atlascloud.ai") + "/public/v1/balance"
	if err := getJSON(ctx, a.HTTP, url, key, &resp); err != nil {
		return usage.Reading{}, err
	}
	// A missing or non-USD balance is an unknown answer, never $0.
	if resp.Object != "balance" || !strings.EqualFold(resp.Available.Currency, "usd") {
		return usage.Reading{}, errors.New("accounts: atlascloud balance: unrecognised answer")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(resp.Available.Value), 64)
	if err != nil {
		return usage.Reading{}, errors.New("accounts: atlascloud balance: unrecognised amount")
	}
	scope := resp.Scope
	if scope == "" {
		scope = "account"
	}
	return usage.Reading{Scope: scope, BalanceUSD: v, HasBalance: true}, nil
}
