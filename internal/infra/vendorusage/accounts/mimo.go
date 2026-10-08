package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMiMo registers the reader (readers_gen.go).
func readerMiMo() usage.Reader { return MiMo{} }

// MiMo reads Xiaomi MiMo's balance and Token Plan from the platform's
// console API with its browser session, as CodexBar's MiMo provider does
// (Sources/CodexBarCore/Providers/MiMo/MiMoUsageFetcher.swift): the
// balance (required), and the plan's monthly credits used and its period
// end (best-effort). A balance in another currency is stored in that
// currency (balance_credits), never converted to dollars.
type MiMo struct {
	BaseURL string
	HTTP    *http.Client
}

func (MiMo) Endpoint() string               { return "mimo" }
func (MiMo) Provider() eventschema.Provider { return "mimo" }
func (MiMo) Source() string                 { return "mimo-web" }

type mimoAnswer struct {
	Code    number          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (m MiMo) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (MiMo is read with the platform's Cookie header)", usage.ErrAuth)
	}
	root := base(m.BaseURL, "https://platform.xiaomimimo.com") + "/api/v1/"
	get := func(path string) (json.RawMessage, error) {
		body, err := doWeb(ctx, m.HTTP, http.MethodGet, root+path, http.Header{
			"Cookie": {cookie}, "Accept": {"application/json, text/plain, */*"}, "Accept-Language": {"en-US,en;q=0.9"},
			"X-Timezone": {"UTC+00:00"}, "Origin": {"https://platform.xiaomimimo.com"},
			"Referer": {"https://platform.xiaomimimo.com/#/console/balance"},
		}, nil)
		if err != nil {
			return nil, err
		}
		var a mimoAnswer
		if err := json.Unmarshal(body, &a); err != nil {
			return nil, fmt.Errorf("accounts: GET %s: %w", hostPath(root+path), err)
		}
		switch {
		case a.Code.ok && (a.Code.v == 401 || a.Code.v == 403):
			return nil, fmt.Errorf("%w (code %.0f on %s)", usage.ErrAuth, a.Code.v, hostPath(root+path))
		case !a.Code.ok || a.Code.v != 0:
			return nil, fmt.Errorf("accounts: GET %s: code %.0f", hostPath(root+path), a.Code.v)
		}
		return a.Data, nil
	}
	raw, err := get("balance")
	if err != nil {
		return usage.Reading{}, err
	}
	var bal struct {
		Balance  number `json:"balance"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(raw, &bal); err != nil || !bal.Balance.ok || strings.TrimSpace(bal.Currency) == "" {
		return usage.Reading{}, errors.New("accounts: mimo: no balance in the answer")
	}
	r := usage.Reading{Scope: "account"}
	if cur := strings.ToUpper(strings.TrimSpace(bal.Currency)); cur == "USD" {
		r.BalanceUSD, r.HasBalance = bal.Balance.v, true
	} else {
		r.Credits, r.CreditsUnit, r.HasCredits = bal.Balance.v, cur, true
	}
	// The Token Plan is best-effort, as the console shows it beside the
	// balance: an account without one, or a failed call, has no window.
	var detail struct {
		CurrentPeriodEnd string `json:"currentPeriodEnd"`
		Expired          bool   `json:"expired"`
	}
	if raw, err := get("tokenPlan/detail"); err == nil {
		_ = json.Unmarshal(raw, &detail)
	}
	var plan struct {
		MonthUsage *struct {
			Items []struct {
				Used    number `json:"used"`
				Limit   number `json:"limit"`
				Percent number `json:"percent"`
			} `json:"items"`
		} `json:"monthUsage"`
	}
	if raw, err := get("tokenPlan/usage"); err == nil && json.Unmarshal(raw, &plan) == nil && !detail.Expired &&
		plan.MonthUsage != nil && len(plan.MonthUsage.Items) > 0 && plan.MonthUsage.Items[0].Limit.v > 0 {
		item := plan.MonthUsage.Items[0]
		w := usage.Window{Name: "month", UsedPct: clampPct(item.Percent.v * 100)}
		if end := timeOf(detail.CurrentPeriodEnd); !end.IsZero() {
			w.ResetsAt, w.Duration = end, end.Sub(end.AddDate(0, -1, 0)).Round(time.Hour)
		}
		r.Subscription, r.Windows = true, []usage.Window{w}
	}
	return r, nil
}
