package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const mistralSession = "ory_session_abc=sess; csrftoken=tok"

func mistralNow() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }

func TestMistralReadsTheAllowances(t *testing.T) {
	srv := newWebServer(t, "ory_session_abc=sess", http.StatusUnauthorized, routesOf(t, "mistral"))
	got, err := Mistral{Admin: srv.URL, Console: srv.URL, Now: mistralNow}.Read(context.Background(), mistralSession)
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	reset := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if w := got.Windows[0]; w.Name != "month (API)" || w.UsedPct != 42.5 || !w.ResetsAt.Equal(reset) {
		t.Errorf("API %+v", w)
	}
	if w := got.Windows[1]; w.Name != "month (Vibe)" || w.UsedPct != 12 {
		t.Errorf("Vibe %+v", w)
	}
	if !got.HasBalance || !approx(got.BalanceUSD, 22.5) {
		t.Errorf("balance %+v", got)
	}
	if srv.paths[0] != "/api/billing/v2/usage" {
		t.Errorf("the session was not proven first: %v", srv.paths)
	}
}

// Without a Vibe allowance on the page, the console's own call supplies
// it, sent only the CSRF and session cookies; a euro balance is kept in
// euros, not reported as dollars.
func TestMistralVibeFallbackAndEuroCredits(t *testing.T) {
	routes := routesOf(t, "mistral")
	routes["/subscription"] = `<script>self.__next_f.push([1,"4:{\"budget\":{\"api_budget\":{\"usage_percentage\":5,\"initial_budget\":10,\"currency\":\"EUR\"}}}\n"])</script>`
	routes["/api-ui/trpc/billing.vibeUsage"] = `[{"result":{"data":{"json":{"usagePercentage":64,"resetAt":"2026-11-01T00:00:00Z"}}}}]`
	routes["/api/billing/credits"] = `{"walletAmount":20,"currency":"EUR"}`
	srv := newWebServer(t, "ory_session_abc=sess", http.StatusUnauthorized, routes)
	got, err := Mistral{Admin: srv.URL, Console: srv.URL, Now: mistralNow}.Read(context.Background(), mistralSession+"; other=private")
	if err != nil || len(got.Windows) != 2 || got.Windows[1].UsedPct != 64 || got.HasBalance || got.CreditsUnit != "EUR" || got.Credits != 20 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestMistralRefusedOrWithoutAllowances(t *testing.T) {
	srv := newWebServer(t, "ory_session_abc=sess", http.StatusUnauthorized, routesOf(t, "mistral"))
	if _, err := (Mistral{Admin: srv.URL, Now: mistralNow}).Read(context.Background(), "ory_session_abc=expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	routes := routesOf(t, "mistral")
	routes["/subscription"] = "<html>no budget here</html>"
	delete(routes, "/api/billing/credits")
	plain := newWebServer(t, "ory_session_abc=sess", http.StatusUnauthorized, routes)
	got, err := Mistral{Admin: plain.URL, Console: plain.URL, Now: mistralNow}.Read(context.Background(), "ory_session_abc=sess")
	if err != nil || !got.Empty() {
		t.Errorf("no allowances = %+v, %v", got, err)
	}
}

// Two different budgets on one page are ambiguous: neither is read.
func TestMistralAmbiguousBudgets(t *testing.T) {
	page := `<script>self.__next_f.push([1,"4:{\"budget\":{\"api_budget\":{\"usage_percentage\":5,\"initial_budget\":10,\"currency\":\"EUR\"}}}\n5:{\"budget\":{\"api_budget\":{\"usage_percentage\":9,\"initial_budget\":10,\"currency\":\"EUR\"}}}\n"])</script>`
	if api, vibe := mistralBudgets([]byte(page)); api != nil || vibe != nil {
		t.Errorf("ambiguous budgets read: %+v %+v", api, vibe)
	}
	if !strings.Contains(fixture(t, "mistral"), "Ta,") {
		t.Error("the fixture no longer carries a byte-counted row")
	}
}
