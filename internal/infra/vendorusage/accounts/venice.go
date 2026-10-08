package accounts

import (
	"context"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerVenice registers the reader (readers_gen.go).
func readerVenice() usage.Reader { return Venice{} }

// Venice reads GET /api/v1/billing/balance
// (https://docs.venice.ai/api-reference/endpoint/billing/balance): the USD
// balance left and, for an account that stakes, the DIEM left of the
// current epoch's allocation, as a window named "epoch" (Venice gives no
// reset time or epoch length in this answer). canConsume false is Venice
// saying requests are blocked. Bundled and earned credits are not dollars
// and are left out.
type Venice struct {
	BaseURL string
	HTTP    *http.Client
}

func (Venice) Endpoint() string               { return "venice" }
func (Venice) Provider() eventschema.Provider { return "venice" }
func (Venice) Source() string                 { return "venice-account" }

func (v Venice) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		CanConsume *bool `json:"canConsume"`
		Balances   *struct {
			Diem number `json:"diem"`
			USD  number `json:"usd"`
		} `json:"balances"`
		Allocation number `json:"diemEpochAllocation"`
	}
	if err := getJSON(ctx, v.HTTP, base(v.BaseURL, "https://api.venice.ai")+"/api/v1/billing/balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.CanConsume == nil || resp.Balances == nil {
		return usage.Reading{}, errors.New("accounts: venice balance: unrecognised answer")
	}
	r := usage.Reading{Scope: "account", LimitReached: !*resp.CanConsume}
	if b := resp.Balances; b.USD.ok {
		r.BalanceUSD, r.HasBalance = b.USD.v, true
	}
	if b := resp.Balances; b.Diem.ok && resp.Allocation.ok && resp.Allocation.v > 0 {
		r.Windows = append(r.Windows, usage.Window{Name: "epoch", UsedPct: clampPct(pct(resp.Allocation.v-b.Diem.v, resp.Allocation.v))})
	}
	return r, nil
}
