package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerFal registers the reader (readers_gen.go).
func readerFal() usage.Reader { return Fal{} }

// Fal reads GET api.fal.ai/v1/account/billing?expand=credits
// (fal.ai/docs/platform-apis/v1/account/billing), ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/fal.js, Providers/Fal,
// docs/fal.md): the account's credit balance, with the key sent as
// "Authorization: Key …". The key needs billing:usage:read (the BILLING,
// READONLY or FULL preset); an inference-only key is refused. An answer
// without its credits block is an error, never $0.
type Fal struct {
	BaseURL string
	HTTP    *http.Client
}

func (Fal) Endpoint() string               { return "fal" }
func (Fal) Provider() eventschema.Provider { return "fal" }
func (Fal) Source() string                 { return "fal-account" }

func (f Fal) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Credits *struct {
			Balance  json.RawMessage `json:"current_balance"`
			Currency string          `json:"currency"`
		} `json:"credits"`
	}
	u := base(f.BaseURL, "https://api.fal.ai") + "/v1/account/billing?expand=credits"
	if err := getJSONAuth(ctx, f.HTTP, u, "Key "+key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Credits == nil {
		return usage.Reading{}, errors.New("accounts: fal billing: no credit balance in the answer")
	}
	balance, ok := falBalance(resp.Credits.Balance)
	if !ok {
		return usage.Reading{}, errors.New("accounts: fal billing: unrecognised credit balance")
	}
	currency := resp.Credits.Currency
	if !currencyCode().MatchString(currency) {
		currency = "USD"
	}
	r := usage.Reading{Scope: "account"}
	setMoney(&r, balance, currency)
	return r, nil
}

// falBalance reads a number or a numeric string.
func falBalance(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || !sonioxMoney().MatchString(strings.TrimSpace(s)) {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v, err == nil
}
