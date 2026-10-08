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

// readerCommandCode registers the reader (readers_gen.go).
func readerCommandCode() usage.Reader { return CommandCode{} }

// CommandCode reads Command Code's billing with its web session (a
// better-auth session cookie), as CodexBar's Command Code provider does
// (Sources/CodexBarCore/Providers/CommandCode/CommandCodeUsageFetcher.swift):
// the 5-hour and weekly rolling limits and the monthly credit grant used,
// from the credits call, the grant sized from the plan when the credits
// call omits it and the period end from the subscription call
// (best-effort); purchased credits are the dollar balance.
type CommandCode struct {
	BaseURL string
	HTTP    *http.Client
}

func (CommandCode) Endpoint() string               { return "commandcode" }
func (CommandCode) Provider() eventschema.Provider { return "commandcode" }
func (CommandCode) Source() string                 { return "commandcode-web" }

// commandCodePlans are the monthly grants, in dollars, of the plans the
// subscription call names (CodexBar's CommandCodePlanCatalog.swift, from
// commandcode.ai/pricing).
var commandCodePlans = map[string]float64{
	"individual-go": 10, "individual-goat": 70, "individual-pro": 30,
	"individual-pro-v1": 80, "individual-max": 150, "individual-ultra": 300,
}

func (c CommandCode) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := strings.TrimSpace(key)
	if !isSession(cookie) {
		// A bare token is the production session cookie's value.
		cookie = "__Secure-commandcode_prod_.session_token=" + cookie
	}
	root := base(c.BaseURL, "https://api.commandcode.ai")
	get := func(path string, out any) error {
		body, err := doWeb(ctx, c.HTTP, http.MethodGet, root+path, http.Header{
			"Cookie": {cookie}, "Accept": {"application/json, text/plain, */*"}, "Accept-Language": {"en-US,en;q=0.9"},
			"Origin": {"https://commandcode.ai"}, "Referer": {"https://commandcode.ai/"},
		}, nil)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("accounts: GET %s: %w", hostPath(root+path), err)
		}
		return nil
	}
	type limit struct {
		Cap     number `json:"cap"`
		Used    number `json:"used"`
		ResetAt stamp  `json:"resetAt"`
	}
	type limits struct {
		FiveHour *limit `json:"fiveHour"`
		Weekly   *limit `json:"weekly"`
	}
	var credits struct {
		Credits *struct {
			MonthlyCredits        number  `json:"monthlyCredits"`
			PurchasedCredits      number  `json:"purchasedCredits"`
			MonthlyCreditsGranted number  `json:"monthlyCreditsGranted"`
			WindowLimits          *limits `json:"windowLimits"`
		} `json:"credits"`
		WindowLimits *limits `json:"windowLimits"`
	}
	if err := get("/internal/billing/credits", &credits); err != nil {
		return usage.Reading{}, err
	}
	if credits.Credits == nil || !credits.Credits.MonthlyCredits.ok {
		return usage.Reading{}, errors.New("accounts: commandcode: no monthly credits in the answer")
	}
	cr := credits.Credits
	r := usage.Reading{Scope: "account", Subscription: true, BalanceUSD: cr.PurchasedCredits.v, HasBalance: cr.PurchasedCredits.ok}
	wl := credits.WindowLimits
	if wl == nil {
		wl = cr.WindowLimits
	}
	if wl != nil {
		for _, w := range []struct {
			l    *limit
			name string
			d    time.Duration
		}{{wl.FiveHour, "5h", 5 * time.Hour}, {wl.Weekly, "week", 7 * 24 * time.Hour}} {
			if w.l != nil && w.l.Cap.v > 0 {
				r.Windows = append(r.Windows, usage.Window{Name: w.name, UsedPct: clampPct(pct(w.l.Used.v, w.l.Cap.v)), Duration: w.d, ResetsAt: w.l.ResetAt.t})
			}
		}
	}
	// The subscription names the plan and the period's end. Only an
	// explicit success is believed; a failure leaves the month unsized
	// unless the credits call sized it.
	var sub struct {
		Success *bool `json:"success"`
		Data    *struct {
			PlanID           string `json:"planId"`
			CurrentPeriodEnd stamp  `json:"currentPeriodEnd"`
		} `json:"data"`
	}
	granted := cr.MonthlyCreditsGranted.v
	var periodEnd time.Time
	if get("/internal/billing/subscriptions", &sub) == nil && sub.Success != nil && *sub.Success && sub.Data != nil {
		periodEnd = sub.Data.CurrentPeriodEnd.t
		if !cr.MonthlyCreditsGranted.ok || granted <= 0 {
			granted = commandCodePlans[strings.ToLower(sub.Data.PlanID)]
		}
	}
	if granted > 0 {
		w := usage.Window{Name: "month", UsedPct: clampPct(pct(granted-cr.MonthlyCredits.v, granted)), ResetsAt: periodEnd}
		if !periodEnd.IsZero() {
			w.Duration = periodEnd.Sub(periodEnd.AddDate(0, -1, 0))
		}
		r.Windows = append(r.Windows, w)
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}
