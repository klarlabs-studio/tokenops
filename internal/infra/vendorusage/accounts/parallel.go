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

// readerParallel registers the reader (readers_gen.go).
func readerParallel() usage.Reader { return Parallel{} }

// Parallel reads the balance platform.parallel.ai's billing page shows,
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/
// parallel.ts, Providers/Parallel, docs/parallel.md): the dashboard's
// internal POST /api/rpc/billing/payments/refresh-balance with the
// browser session, in US cents. Parallel's API keys cannot read it, and
// its documented Account API needs an OAuth sign-in. The endpoint is
// undocumented: any other shape is an error, never $0.
type Parallel struct {
	BaseURL string
	HTTP    *http.Client
}

func (Parallel) Endpoint() string               { return "parallel" }
func (Parallel) Provider() eventschema.Provider { return "parallel" }
func (Parallel) Source() string                 { return "parallel-web" }

func (p Parallel) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (the Parallel credential is not a Cookie header; its API keys cannot read the balance)", usage.ErrAuth)
	}
	origin := base(p.BaseURL, "https://platform.parallel.ai")
	resp, err := doWebResponse(ctx, p.HTTP, http.MethodPost, origin+"/api/rpc/billing/payments/refresh-balance", http.Header{
		"Cookie": {cookie}, "Origin": {origin}, "Content-Type": {"application/json"}, "Accept": {"application/json"},
	}, []byte(`{}`))
	if err != nil {
		return usage.Reading{}, err
	}
	if ct := resp.header.Get("Content-Type"); len(ct) >= 9 && ct[:9] == "text/html" {
		return usage.Reading{}, fmt.Errorf("%w (Parallel answered its sign-in page)", usage.ErrAuth)
	}
	var out struct {
		Kind string `json:"kind"`
		Data *struct {
			Balance *float64 `json:"balance"`
		} `json:"data"`
	}
	if json.Unmarshal(resp.body, &out) != nil || out.Kind != "ok" || out.Data == nil || out.Data.Balance == nil {
		return usage.Reading{}, errors.New("accounts: parallel balance: unrecognised answer")
	}
	return usage.Reading{Scope: "account", BalanceUSD: *out.Data.Balance / 100, HasBalance: true}, nil
}
