package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerTinyAPI registers the reader (readers_gen.go).
func readerTinyAPI() usage.Reader { return TinyAPI{} }

// TinyAPI reads the credits tinyapi.rest's dashboard shows, ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/tinyapi.js,
// Providers/TinyApi, docs/tinyapi.md): GET /api/user/credits with the
// browser session. Its tool-execution API keys cannot read it. The total
// available is kept in credits, never converted to dollars; free and
// purchased credits are not told apart, and no allowance or renewal is
// inferred.
type TinyAPI struct {
	BaseURL string
	HTTP    *http.Client
}

func (TinyAPI) Endpoint() string               { return "tinyapi" }
func (TinyAPI) Provider() eventschema.Provider { return "tinyapi" }
func (TinyAPI) Source() string                 { return "tinyapi-web" }

func (t TinyAPI) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (the TinyApi credential is not a Cookie header)", usage.ErrAuth)
	}
	body, err := doWeb(ctx, t.HTTP, http.MethodGet, base(t.BaseURL, "https://tinyapi.rest")+"/api/user/credits",
		http.Header{"Cookie": {cookie}, "Accept": {"application/json"}}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	var resp struct {
		Success bool `json:"success"`
		Data    *struct {
			Total *float64 `json:"totalAvailable"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &resp) != nil || !resp.Success || resp.Data == nil || resp.Data.Total == nil || *resp.Data.Total < 0 {
		return usage.Reading{}, errors.New("accounts: tinyapi credits: unrecognised answer")
	}
	return usage.Reading{Scope: "account", Credits: *resp.Data.Total, CreditsUnit: "credits", HasCredits: true}, nil
}
