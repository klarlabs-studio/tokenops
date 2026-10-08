package accounts

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerGrok registers the reader (readers_gen.go).
func readerGrok() usage.Reader { return Grok{} }

// Grok reads a Grok (SuperGrok) subscription's credit usage with the grok
// CLI's own sign-in, granted by the operator (ADR 0013), from the CLI's
// billing proxy, as CodexBar's Grok provider does: the share of the
// billing period's credits used, when the period resets, and the prepaid
// balance. The token is never refreshed here; the CLI renews it.
type Grok struct {
	BaseURL string
	HTTP    *http.Client
}

func (Grok) Endpoint() string               { return "grok" }
func (Grok) Provider() eventschema.Provider { return "grok" }
func (Grok) Source() string                 { return "grok-account" }

// grokAmount is the proxy's {"val": n}; val may be a number or a string.
type grokAmount struct {
	Val number `json:"val"`
}

func (g Grok) Read(ctx context.Context, token string) (usage.Reading, error) {
	var resp struct {
		Config *struct {
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			CurrentPeriod      *struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"currentPeriod"`
			BillingPeriodStart string      `json:"billingPeriodStart"`
			BillingPeriodEnd   string      `json:"billingPeriodEnd"`
			OnDemandCap        *grokAmount `json:"onDemandCap"`
			OnDemandUsed       *grokAmount `json:"onDemandUsed"`
			PrepaidBalance     *grokAmount `json:"prepaidBalance"`
		} `json:"config"`
	}
	header := http.Header{"Authorization": {"Bearer " + token}, "x-xai-token-auth": {"xai-grok-cli"}}
	if err := doJSON(ctx, g.HTTP, http.MethodGet, base(g.BaseURL, "https://cli-chat-proxy.grok.com")+"/v1/billing?format=credits", header, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	c := resp.Config
	if c == nil {
		return usage.Reading{}, errors.New("accounts: GET cli-chat-proxy.grok.com/v1/billing: unexpected answer")
	}
	// The period's end, and the start of that same period: never a start
	// from one period with the end of another.
	end, start := time.Time{}, time.Time{}
	if c.CurrentPeriod != nil {
		end = parseTime(c.CurrentPeriod.End)
		start = parseTime(c.CurrentPeriod.Start)
	}
	if end.IsZero() {
		end, start = parseTime(c.BillingPeriodEnd), parseTime(c.BillingPeriodStart)
	}
	used, ok := 0.0, false
	switch {
	case c.CreditUsagePercent != nil && !math.IsNaN(*c.CreditUsagePercent) && !math.IsInf(*c.CreditUsagePercent, 0):
		used, ok = *c.CreditUsagePercent, true
	case c.OnDemandCap != nil && c.OnDemandCap.Val.ok && c.OnDemandCap.Val.v > 0 && c.OnDemandUsed != nil && c.OnDemandUsed.Val.ok:
		used, ok = pct(c.OnDemandUsed.Val.v, c.OnDemandCap.Val.v), true
	}
	r := usage.Reading{Scope: "account"}
	if ok {
		w := usage.Window{UsedPct: clampPct(used), ResetsAt: end}
		if !start.IsZero() && end.After(start) {
			w.Duration = end.Sub(start).Truncate(time.Minute)
		}
		w.Name = grokWindowName(w.Duration)
		r.Windows, r.Subscription = []usage.Window{w}, true
	}
	if c.PrepaidBalance != nil && c.PrepaidBalance.Val.ok && c.PrepaidBalance.Val.v >= 0 {
		// The billing contract gives the wallet in US cents.
		r.BalanceUSD, r.HasBalance = c.PrepaidBalance.Val.v/100, true
	}
	return r, nil
}

// grokWindowName names the billing period: a month when it is one, else
// by its length in days.
func grokWindowName(d time.Duration) string {
	switch {
	case d <= 0:
		return "period"
	case d >= 28*24*time.Hour && d <= 31*24*time.Hour:
		return "month"
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return windowName(d)
}
