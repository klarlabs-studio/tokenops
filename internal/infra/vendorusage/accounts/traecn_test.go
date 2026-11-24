package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func traeServer(t *testing.T, tokenBody, usageBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Cookie") != "sessionid=fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/cloudide/api/v3/common/GetUserToken":
			_, _ = w.Write([]byte(tokenBody))
		case "/trae/api/v2/pay/user_current_entitlement_list":
			if r.Header.Get("Authorization") != "Cloud-IDE-JWT jwt-fixture" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(usageBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestTraeCNCreditsUsedAcrossPacks(t *testing.T) {
	srv := traeServer(t, `{"Result":{"Token":"jwt-fixture"}}`, fixture(t, "trae-cn"))
	defer srv.Close()
	got, err := TraeCN{BaseURL: srv.URL}.Read(context.Background(), "sessionid=fixture")
	if err != nil || !got.Subscription || len(got.Windows) != 1 || got.Windows[0].Name != "credits" ||
		!approx(got.Windows[0].UsedPct, 20) || !got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestTraeCNShapes(t *testing.T) {
	for _, tc := range []struct {
		token, body string
		ok, auth    bool
		windows     int
	}{
		{`{"Result":{"Token":"jwt-fixture"}}`, `{"is_credits_billing":true,"usage_summary":{"total_amount":16400,"consumed_amount":5677.34}}`, true, false, 1},
		{`{"Result":{"Token":"jwt-fixture"}}`, `{"is_credits_billing":true,"usage_summary":{"total_amount":0,"consumed_amount":0}}`, true, false, 0},
		{`{"Result":{"Token":"jwt-fixture"}}`, `{"code":1001}`, false, true, 0},
		{`{"Result":{"Token":"jwt-fixture"}}`, `{"is_credits_billing":false,"usage_summary":{"total_amount":1,"consumed_amount":0}}`, false, false, 0},
		{`{"Result":{"Token":"jwt-fixture"}}`, `{"is_credits_billing":true,"usage_summary":{"total_amount":-1,"consumed_amount":0}}`, false, false, 0},
		{`{"Result":{}}`, `{}`, false, false, 0},
	} {
		srv := traeServer(t, tc.token, tc.body)
		got, err := TraeCN{BaseURL: srv.URL}.Read(context.Background(), "sessionid=fixture")
		srv.Close()
		if (err == nil) != tc.ok || errors.Is(err, usage.ErrAuth) != tc.auth || (tc.ok && len(got.Windows) != tc.windows) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
