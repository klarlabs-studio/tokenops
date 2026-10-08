package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const replicatePage = `<html><head><title>Billing | Replicate</title></head><body>
<script id="react-component-props-billing" type="application/json">{"props":{"viewer":{"name":"x"},"account":{"kind":"organization","username":"acme-labs"}}}</script>
</body></html>`

const replicateSignedOut = `<html><head><title>Sign in | Replicate</title></head><body><a href="/login/github/?next=/account/billing">GitHub</a></body></html>`

// replicateServer serves the billing page and the account API to the
// session "rs"; page is the billing page's HTML.
func replicateServer(t *testing.T, page, invoices, credit string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookieValue(r.Header.Get("Cookie"), "sessionid") != "rs" {
			http.Redirect(w, r, "/signin", http.StatusFound)
			return
		}
		body := map[string]string{
			"/account/billing":                           page,
			"/api/organizations/acme-labs/invoices":      invoices,
			"/api/organizations/acme-labs/unused-credit": credit,
		}[r.URL.Path]
		if body == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var replicateNow = func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }

func TestReplicateReadsTheBilling(t *testing.T) {
	srv := replicateServer(t, replicatePage, fixture(t, "replicate"), `{"unused_credit":"35.73"}`)
	for _, cred := range []string{"csrftoken=c; sessionid=rs", "rs"} {
		got, err := Replicate{BaseURL: srv.URL, Now: replicateNow}.Read(context.Background(), cred)
		if err != nil || !got.HasUsed || !approx(got.UsedUSD, 14.27) || got.LimitUSD != 0 || got.Subscription {
			t.Fatalf("%q: %+v, %v", cred, got, err)
		}
		if !got.HasBalance || !approx(got.BalanceUSD, 35.73) {
			t.Errorf("balance %+v", got)
		}
	}
}

// The unused credit is best effort: without it the spend still reads.
func TestReplicateWithoutCredit(t *testing.T) {
	srv := replicateServer(t, replicatePage, fixture(t, "replicate"), "")
	got, err := Replicate{BaseURL: srv.URL, Now: replicateNow}.Read(context.Background(), "rs")
	if err != nil || !got.HasUsed || got.HasBalance {
		t.Errorf("%+v, %v", got, err)
	}
}

func TestReplicateRefusals(t *testing.T) {
	srv := replicateServer(t, replicatePage, fixture(t, "replicate"), "")
	for _, cred := range []string{"sessionid=old", "csrftoken=c"} {
		if _, err := (Replicate{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
	out := replicateServer(t, replicateSignedOut, fixture(t, "replicate"), "")
	if _, err := (Replicate{BaseURL: out.URL}).Read(context.Background(), "rs"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("signed-out page = %v, want ErrAuth", err)
	}
}

// Changed props or no current invoice are errors, never zero spend.
func TestReplicateUnknownShapes(t *testing.T) {
	cases := []*httptest.Server{
		replicateServer(t, strings.ReplaceAll(replicatePage, `"account"`, `"owner"`), fixture(t, "replicate"), ""),
		replicateServer(t, replicatePage, `{"invoices":[{"type":"monthly-usage","ended_before":"2026-10-01T00:00:00Z","total_cost_before_adjustments":"1"}]}`, ""),
		replicateServer(t, replicatePage, `{"results":[]}`, ""),
	}
	for i, srv := range cases {
		if got, err := (Replicate{BaseURL: srv.URL, Now: replicateNow}).Read(context.Background(), "rs"); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("case %d: %+v, %v", i, got, err)
		}
	}
}
