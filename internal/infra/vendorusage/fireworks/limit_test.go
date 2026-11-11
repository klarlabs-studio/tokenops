package fireworks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// accountAPI serves an account with $15 spent this month. quotas is the
// body of its quota list, "" for a failed read; account is the body of
// the account itself, "" likewise.
func accountAPI(quotas, account string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := ""
		switch r.URL.Path {
		case "/v1/accounts/acme/billing/summary":
			body = `{"lineItems":[{"totalCost":{"currencyCode":"USD","units":"15"}}]}`
		case "/v1/accounts/acme/quotas":
			body = quotas
		case "/v1/accounts/acme":
			body = account
		}
		if body == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

// Only a quota list read whole, with no spend quota in it, says the
// account has no spend limit. A limit that was not read is unknown, and
// the spend that was read still stands.
func TestAccountLimitNotReadIsUnknown(t *testing.T) {
	const active = `{"name":"accounts/acme","suspendState":"UNSUSPENDED"}`
	tests := []struct {
		name        string
		quotas      string
		account     string
		wantLimit   float64
		wantUnknown bool
		wantReached bool
	}{
		{name: "a spend quota is the limit",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/monthly-spend-usd","value":"200"}]}`, account: active, wantLimit: 200},
		{name: "a quota of zero is a limit of zero",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/monthly-spend-usd","value":"0"}]}`, account: active},
		{name: "a whole list without one is no limit",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/h100-us-iowa-1","value":"8"}]}`, account: active},
		{name: "a failed quota read", quotas: "", account: active, wantUnknown: true},
		{name: "a value that is no number",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/monthly-spend-usd","value":"unlimited"}]}`, account: active, wantUnknown: true},
		{name: "a spend quota with no value",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/monthly-spend-usd"}]}`, account: active, wantUnknown: true},
		{name: "a list cut short without one",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/h100-us-iowa-1","value":"8"}],"nextPageToken":"p2"}`, account: active, wantUnknown: true},
		{name: "a list cut short after the spend quota",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/monthly-spend-usd","value":"200"}],"nextPageToken":"p2"}`, account: active, wantLimit: 200},
		{name: "a failed account read claims no block",
			quotas: `{"quotas":[{"name":"accounts/acme/quotas/monthly-spend-usd","value":"200"}]}`, account: "", wantLimit: 200},
		{name: "a suspended account is blocked",
			quotas:  `{"quotas":[{"name":"accounts/acme/quotas/monthly-spend-usd","value":"200"}]}`,
			account: `{"suspendState":"MONTHLY_SPEND_LIMIT_EXCEEDED"}`, wantLimit: 200, wantReached: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := accountAPI(tt.quotas, tt.account)
			defer srv.Close()
			r, err := client(srv).AccountMonth(context.Background(), "acme", now)
			if err != nil {
				t.Fatal(err)
			}
			if r.UsedUSD != 15 {
				t.Errorf("spent $%v, want the $15 the summary reports", r.UsedUSD)
			}
			if r.LimitUSD != tt.wantLimit || r.LimitUnknown != tt.wantUnknown || r.LimitReached != tt.wantReached {
				t.Errorf("limit $%v unknown %v reached %v, want $%v %v %v",
					r.LimitUSD, r.LimitUnknown, r.LimitReached, tt.wantLimit, tt.wantUnknown, tt.wantReached)
			}
		})
	}
}
