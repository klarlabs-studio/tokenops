package accounts

import (
	"context"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerPoe registers the reader (readers_gen.go).
func readerPoe() usage.Reader { return Poe{} }

// Poe reads GET /usage/current_balance: the points left, plan and add-on
// points together (https://creator.poe.com/docs/resources/usage-api). Poe
// bills in points and publishes no rate to dollars, so the balance is kept
// in points and never converted.
type Poe struct {
	BaseURL string
	HTTP    *http.Client
}

func (Poe) Endpoint() string               { return "poe" }
func (Poe) Provider() eventschema.Provider { return "poe" }
func (Poe) Source() string                 { return "poe-account" }

func (p Poe) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Balance number `json:"current_point_balance"`
	}
	if err := getJSON(ctx, p.HTTP, base(p.BaseURL, "https://api.poe.com")+"/usage/current_balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if !resp.Balance.ok {
		return usage.Reading{}, errors.New("accounts: poe balance: no current_point_balance")
	}
	return usage.Reading{Scope: "account", Credits: resp.Balance.v, CreditsUnit: "points", HasCredits: true}, nil
}
