package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerXAI registers the reader (readers_gen.go).
func readerXAI() usage.Reader { return XAI{} }

// XAI reads a team's prepaid credit from xAI's Management API, GET
// /v1/billing/teams/{team_id}/prepaid/balance
// (https://docs.x.ai/developers/rest-api-reference/management/billing).
// The credential is "TEAM_ID:MANAGEMENT_KEY": the Management API needs
// both, and refuses inference keys. The ledger is inverted, in USD cents
// as a string (a $10 top-up is "-1000"), so the balance is the negated
// total. It is the posted ledger: spend is posted at the billing cycle's
// close, so mid-cycle it can be above the console's live figure.
type XAI struct {
	BaseURL string
	HTTP    *http.Client
}

func (XAI) Endpoint() string               { return "xai" }
func (XAI) Provider() eventschema.Provider { return eventschema.ProviderXAI }
func (XAI) Source() string                 { return "xai-account" }

func (x XAI) Read(ctx context.Context, credential string) (usage.Reading, error) {
	team, key, ok := strings.Cut(strings.TrimSpace(credential), ":")
	team, key = strings.TrimSpace(team), strings.TrimSpace(key)
	if !ok || team == "" || key == "" || strings.ContainsAny(team, "/?#") || team == "." || team == ".." {
		return usage.Reading{}, fmt.Errorf("%w (xAI needs TEAM_ID:MANAGEMENT_KEY)", usage.ErrAuth)
	}
	var resp struct {
		Total *struct {
			Val string `json:"val"`
		} `json:"total"`
	}
	u := base(x.BaseURL, "https://management-api.x.ai") + "/v1/billing/teams/" + url.PathEscape(team) + "/prepaid/balance"
	if err := getJSON(ctx, x.HTTP, u, key, &resp); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusNotFound {
			return usage.Reading{}, fmt.Errorf("%w (xAI knows no team %q for this management key)", usage.ErrAuth, team)
		}
		return usage.Reading{}, err
	}
	if resp.Total == nil {
		return usage.Reading{}, errors.New("accounts: xai balance: no total")
	}
	cents, err := strconv.ParseFloat(strings.TrimSpace(resp.Total.Val), 64)
	if err != nil {
		return usage.Reading{}, errors.New("accounts: xai balance: unreadable total")
	}
	return usage.Reading{Scope: "team", BalanceUSD: -cents / 100, HasBalance: true}, nil
}
