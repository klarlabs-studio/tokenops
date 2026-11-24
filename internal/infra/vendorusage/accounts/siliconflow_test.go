package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestSiliconFlowReadsTheInternationalTotalBalance(t *testing.T) {
	srv := serve(t, "/v1/user/info", "sk-sf", fixture(t, "siliconflow"))
	defer srv.Close()
	got, err := SiliconFlow{BaseURL: srv.URL}.Read(context.Background(), "sk-sf")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 88.88) || got.Scope != "account" {
		t.Fatalf("%+v %v", got, err)
	}
	// A session is the web reader's, never sent to the API.
	if _, err := (SiliconFlow{BaseURL: srv.URL}).Read(context.Background(), "__SF_auth.session-token=x"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("session: %v", err)
	}
}

func TestSiliconFlowShapes(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		auth    bool
		balance float64
	}{
		// An overdue account keeps its negative total.
		{`{"code":20000,"status":true,"data":{"balance":"0","chargeBalance":"0","totalBalance":"-1.50","status":"overdue"}}`, true, false, -1.5},
		{`{"code":20000,"status":true,"data":{"totalBalance":12.5}}`, true, false, 12.5},
		{`{"code":20000,"status":true,"data":{"totalBalance":"88.88.1"}}`, false, false, 0},
		{`{"code":20000,"status":true,"data":{}}`, false, false, 0},
		{`{"code":30014,"message":"invalid key"}`, false, true, 0},
		{`{"code":20092,"message":"retired"}`, false, false, 0},
		{`{"message":"OK"}`, false, false, 0},
	} {
		srv := serve(t, "/v1/user/info", "k", tc.body)
		got, err := SiliconFlow{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || errors.Is(err, usage.ErrAuth) != tc.auth || (tc.ok && !approx(got.BalanceUSD, tc.balance)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}

// siliconFlowConsole serves the console: the billing page answers the
// subject in a header, the wallet reads it.
func siliconFlowConsole(t *testing.T, subject, wallet string, redirect bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "__SF_auth.session-token=fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/me/expensebill":
			if redirect {
				w.Header().Set("Location", "https://account.siliconflow.cn/login")
				w.WriteHeader(http.StatusFound)
				return
			}
			if subject != "" {
				w.Header().Set("x-subject-id", subject)
			}
			_, _ = w.Write([]byte("<html></html>"))
		case "/walletd-server/api/v1/subject/profile/peek":
			if r.Header.Get("x-subject-id") != subject {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(wallet))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestSiliconFlowWebReadsTheConsoleBalanceInYuan(t *testing.T) {
	srv := siliconFlowConsole(t, "subject_1", fixture(t, "siliconflow-web"), false)
	defer srv.Close()
	got, err := SiliconFlowWeb{BaseURL: srv.URL}.Read(context.Background(), "__SF_auth.session-token=fixture")
	if err != nil || !got.HasCredits || !approx(got.Credits, 12.3456789) || got.CreditsUnit != "CNY" || got.HasBalance {
		t.Fatalf("%+v %v", got, err)
	}
	// An API key is the key reader's.
	if _, err := (SiliconFlowWeb{BaseURL: srv.URL}).Read(context.Background(), "sk-sf"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("key: %v", err)
	}
}

func TestSiliconFlowWebShapes(t *testing.T) {
	for _, tc := range []struct {
		name, subject, wallet string
		redirect, ok, auth    bool
		yuan                  float64
	}{
		{"negative", "s", `{"code":20000,"data":{"financialInfo":{"balance":"-1500000000000"}}}`, false, true, false, -1.5},
		{"tiny", "s", `{"code":20000,"data":{"financialInfo":{"balance":"5"}}}`, false, true, false, 0.000000000005},
		{"signed out", "s", ``, true, false, true, 0},
		{"no subject", "", `{}`, false, false, false, 0},
		{"fractional", "s", `{"code":20000,"data":{"financialInfo":{"balance":"1.5"}}}`, false, false, false, 0},
		{"number", "s", `{"code":20000,"data":{"financialInfo":{"balance":15}}}`, false, false, false, 0},
		{"empty", "s", `{"code":20000,"data":{}}`, false, false, false, 0},
		{"code", "s", `{"code":1,"data":{"financialInfo":{"balance":"1"}}}`, false, false, false, 0},
	} {
		srv := siliconFlowConsole(t, tc.subject, tc.wallet, tc.redirect)
		got, err := SiliconFlowWeb{BaseURL: srv.URL}.Read(context.Background(), "__SF_auth.session-token=fixture")
		srv.Close()
		if (err == nil) != tc.ok || errors.Is(err, usage.ErrAuth) != tc.auth || (tc.ok && got.Credits != tc.yuan) {
			t.Errorf("%s: %+v %v", tc.name, got, err)
		}
	}
}
