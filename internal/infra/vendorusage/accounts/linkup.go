package accounts

import (
	"context"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerLinkup registers the reader (readers_gen.go).
func readerLinkup() usage.Reader { return Linkup{} }

// Linkup reads GET api.linkup.so/v1/credits/balance
// (docs.linkup.so, "Credit balance"), ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/linkup.js, Providers/Linkup,
// docs/linkup.md): the prepaid credit, in USD since Linkup's May 2026
// move to dollar pricing. An eligible account is topped back up to $20 a
// month; the endpoint does not say when, so no window is claimed.
type Linkup struct {
	BaseURL string
	HTTP    *http.Client
}

func (Linkup) Endpoint() string               { return "linkup" }
func (Linkup) Provider() eventschema.Provider { return "linkup" }
func (Linkup) Source() string                 { return "linkup-account" }

func (l Linkup) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Balance *float64 `json:"balance"`
	}
	if err := getJSON(ctx, l.HTTP, base(l.BaseURL, "https://api.linkup.so")+"/v1/credits/balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Balance == nil {
		return usage.Reading{}, errors.New("accounts: linkup balance: unrecognised answer")
	}
	// Zero and negative balances are kept.
	return usage.Reading{Scope: "account", BalanceUSD: *resp.Balance, HasBalance: true}, nil
}
