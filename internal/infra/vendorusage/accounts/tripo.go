package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerTripo registers the reader (readers_gen.go).
func readerTripo() usage.Reader { return Tripo{} }

// Tripo reads GET openapi.tripo3d.ai/v3/account/balance
// (developers.tripo3d.ai/en/docs/account), ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/tripo.ts, Providers/Tripo,
// docs/tripo.md): the credits available, already less those frozen for
// tasks in progress. Credits stay credits, never converted to money.
type Tripo struct {
	BaseURL string
	HTTP    *http.Client
}

func (Tripo) Endpoint() string               { return "tripo" }
func (Tripo) Provider() eventschema.Provider { return "tripo" }
func (Tripo) Source() string                 { return "tripo-account" }

func (t Tripo) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Code *int `json:"code"`
		Data *struct {
			Balance *float64 `json:"balance"`
			Frozen  *float64 `json:"frozen"`
		} `json:"data"`
	}
	if err := getJSON(ctx, t.HTTP, base(t.BaseURL, "https://openapi.tripo3d.ai")+"/v3/account/balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	switch {
	case resp.Code == nil:
		return usage.Reading{}, errors.New("accounts: tripo balance: unrecognised answer")
	case *resp.Code != 0:
		return usage.Reading{}, fmt.Errorf("accounts: tripo balance: code %d", *resp.Code)
	}
	d := resp.Data
	if d == nil || d.Balance == nil || d.Frozen == nil || *d.Balance < 0 || *d.Frozen < 0 {
		return usage.Reading{}, errors.New("accounts: tripo balance: unrecognised answer")
	}
	return usage.Reading{Scope: "account", Credits: *d.Balance, CreditsUnit: "credits", HasCredits: true}, nil
}
