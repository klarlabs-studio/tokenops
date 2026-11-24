package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerFlora registers the reader (readers_gen.go).
func readerFlora() usage.Reader { return Flora{} }

// Flora reads a FLORA workspace's balance and this month's spend,
// GET app.flora.ai/api/v1/workspaces/{id}/usage (developer.flora.ai,
// "Get workspace balance and usage"), ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/flora.js, Providers/Flora,
// docs/flora.md). The key needs billing permission on the workspace; its
// workspace list can come back empty, so the workspace is named: the
// credential is WORKSPACE_ID:API_KEY. available_cost is the workspace's
// pool, or the caller's own allowance, as FLORA scopes it.
type Flora struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (Flora) Endpoint() string               { return "flora" }
func (Flora) Provider() eventschema.Provider { return "flora" }
func (Flora) Source() string                 { return "flora-workspace" }

var floraWorkspace = lazyRegexp(`^ws_[A-Za-z0-9]+$`)

func (f Flora) Read(ctx context.Context, credential string) (usage.Reading, error) {
	ws, key, ok := strings.Cut(strings.TrimSpace(credential), ":")
	ws, key = strings.TrimSpace(ws), strings.TrimSpace(key)
	if !ok || key == "" || !floraWorkspace().MatchString(ws) {
		return usage.Reading{}, fmt.Errorf("%w (FLORA needs WORKSPACE_ID:API_KEY, the workspace as ws_…)", usage.ErrAuth)
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	t := now().UTC()
	u := fmt.Sprintf("%s/api/v1/workspaces/%s/usage?start_date=%s&end_date=%s", base(f.BaseURL, "https://app.flora.ai"),
		ws, t.Format("2006-01")+"-01", t.Format("2006-01-02"))
	var resp struct {
		Available    *float64 `json:"available_cost"`
		Currency     string   `json:"currency"`
		BalanceScope string   `json:"balance_scope"`
		Spend        *struct {
			Status string   `json:"status"`
			Total  *float64 `json:"total_cost"`
		} `json:"spend"`
	}
	err := getJSON(ctx, f.HTTP, u, key, &resp)
	var se *statusError
	if errors.As(err, &se) && se.status == http.StatusNotFound {
		return usage.Reading{}, fmt.Errorf("%w (FLORA knows no workspace %s for this key)", usage.ErrAuth, ws)
	}
	if err != nil {
		return usage.Reading{}, err
	}
	if resp.Available == nil {
		return usage.Reading{}, errors.New("accounts: flora usage: unrecognised answer")
	}
	currency := resp.Currency
	if !currencyCode().MatchString(currency) {
		currency = "USD"
	}
	scope := "workspace"
	if resp.BalanceScope == "caller" {
		scope = "member"
	}
	r := usage.Reading{Scope: scope}
	setMoney(&r, *resp.Available, currency)
	if resp.Spend != nil && resp.Spend.Status == "available" {
		if resp.Spend.Total == nil {
			return usage.Reading{}, errors.New("accounts: flora usage: no spend total")
		}
		if strings.EqualFold(currency, "USD") {
			r.UsedUSD, r.HasUsed = *resp.Spend.Total, true
		}
	}
	return r, nil
}
