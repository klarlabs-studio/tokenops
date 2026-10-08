package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerNous registers the reader (readers_gen.go).
func readerNous() usage.Reader { return Nous{} }

// Nous reads Nous Portal's GET /api/oauth/account, as CodexBar's Nous
// provider does: the subscription's monthly credit grant used this billing
// period, and the top-up credit left, both in dollars. The endpoint takes
// a Portal access token from Hermes Agent's sign-in; inference API keys are
// refused. The token lasts about an hour and is never refreshed here
// (refresh tokens rotate, and a replay signs Hermes out); an expired one is
// refused before any request.
type Nous struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock the token's expiry is checked against; nil is
	// time.Now.
	Now func() time.Time
}

func (Nous) Endpoint() string               { return "nous" }
func (Nous) Provider() eventschema.Provider { return "nous" }
func (Nous) Source() string                 { return "nous-account" }

func (n Nous) Read(ctx context.Context, key string) (usage.Reading, error) {
	now := time.Now
	if n.Now != nil {
		now = n.Now
	}
	if exp, ok := jwtExpiry(key); ok && !exp.After(now().Add(time.Minute)) {
		return usage.Reading{}, fmt.Errorf("%w (the Nous Portal access token has expired; run hermes to renew it)", usage.ErrAuth)
	}
	var resp struct {
		Error        json.RawMessage `json:"error"`
		Subscription *struct {
			MonthlyCredits   number `json:"monthly_credits"`
			CreditsRemaining number `json:"credits_remaining"`
			CurrentPeriodEnd string `json:"current_period_end"`
		} `json:"subscription"`
		PurchasedCreditsRemaining number `json:"purchased_credits_remaining"`
		PaidServiceAccess         *struct {
			SubscriptionCreditsRemaining number `json:"subscription_credits_remaining"`
			PurchasedCreditsRemaining    number `json:"purchased_credits_remaining"`
			TotalUsableCredits           number `json:"total_usable_credits"`
		} `json:"paid_service_access"`
	}
	if err := getJSON(ctx, n.HTTP, base(n.BaseURL, "https://portal.nousresearch.com")+"/api/oauth/account", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" && string(resp.Error) != "false" {
		return usage.Reading{}, errors.New("accounts: GET portal.nousresearch.com/api/oauth/account: the portal reported an error")
	}
	var access struct{ sub, purchased, total number }
	if p := resp.PaidServiceAccess; p != nil {
		access.sub, access.purchased, access.total = p.SubscriptionCreditsRemaining, p.PurchasedCreditsRemaining, p.TotalUsableCredits
	}
	r := usage.Reading{Scope: "account"}
	if s := resp.Subscription; s != nil && s.MonthlyCredits.ok && s.MonthlyCredits.v > 0 {
		// A monthly meter needs both the grant and what is left of it.
		if left := firstNumber(s.CreditsRemaining, access.sub); left.ok {
			r.Subscription = true
			r.Windows = []usage.Window{{Name: "month", UsedPct: clampPct(pct(s.MonthlyCredits.v-max(0, left.v), s.MonthlyCredits.v)), ResetsAt: parseTime(s.CurrentPeriodEnd)}}
		}
	}
	if b := firstNumber(resp.PurchasedCreditsRemaining, access.purchased, access.total); b.ok {
		r.BalanceUSD, r.HasBalance = b.v, true
	}
	return r, nil
}

// jwtExpiry is a JWT's exp claim, when key is one.
func jwtExpiry(key string) (time.Time, bool) {
	parts := strings.Split(key, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}
